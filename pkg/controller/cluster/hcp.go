package cluster

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"strconv"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

// findNonLoopbackSAN returns the first non-loopback address from the given
// TLS SANs. Returns empty string if none is found.
func findNonLoopbackSAN(sans []string) string {
	for _, san := range sans {
		if san == "localhost" {
			continue
		}

		if ip := net.ParseIP(san); ip != nil && ip.IsLoopback() {
			continue
		}

		return san
	}

	return ""
}

// ensureHCPKubernetesEndpointSlice maintains the default/kubernetes Service
// EndpointSlice inside the virtual cluster, pointing it at the externally
// reachable addresses (NodePort / LoadBalancer / Ingress) so that pods
// scheduled on external worker nodes can reach the in-cluster apiserver
// ClusterIP. See hcpEndpointAddresses for how those are picked.
//
// Background: the kube-apiserver normally reconciles default/kubernetes
// EndpointSlice to its own --advertise-address:--secure-port (the host-cluster
// pod IP and 6443). External worker nodes have no route to the host-cluster
// pod CIDR, so kube-proxy DNAT to that endpoint fails. We disable the
// apiserver reconciler in HCP mode (see serverOptions) and own this
// EndpointSlice object instead.
func (c *Reconciler) ensureHCPKubernetesEndpointSlice(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	ips, port, err := c.hcpEndpointAddresses(ctx, cluster)
	if err != nil {
		return err
	}

	var addressType discoveryv1.AddressType
	if isIPv4(ips[0]) {
		addressType = discoveryv1.AddressTypeIPv4
	} else {
		addressType = discoveryv1.AddressTypeIPv6
	}

	virtClient, err := newVirtualClient(ctx, c.Client, cluster.Name, cluster.Namespace)
	if err != nil {
		return fmt.Errorf("creating virtual cluster client: %w", err)
	}

	endpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: metav1.NamespaceDefault,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, virtClient, endpointSlice, func() error {
		if endpointSlice.Labels == nil {
			endpointSlice.Labels = make(map[string]string)
		}

		// Ensure the service-name label is set
		endpointSlice.Labels[discoveryv1.LabelServiceName] = "kubernetes"
		endpointSlice.AddressType = addressType

		var endpoints []discoveryv1.Endpoint
		for _, nodeIP := range ips {
			endpoints = append(endpoints, discoveryv1.Endpoint{
				Addresses: []string{nodeIP},
			})
		}

		endpointSlice.Endpoints = endpoints

		endpointSlice.Ports = []discoveryv1.EndpointPort{
			{
				Name:     new("https"),
				Port:     new(port),
				Protocol: new(corev1.ProtocolTCP),
			},
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("upserting default/kubernetes endpointslice in virtual cluster: %w", err)
	}

	log.V(1).Info("HCP kubernetes endpointslice reconciled", "ips", ips, "port", port)

	return nil
}

// hcpEndpointAddresses returns the addresses and the port that both the default/kubernetes
// Endpoints and EndpointSlice publish: one host node IP per server when the cluster is
// exposed via NodePort, so that every server is individually addressable, otherwise the
// single externally resolved address.
//
// Both objects have to describe the same thing: a client picking one over the other
// must not end up with a different set of servers.
func (c *Reconciler) hcpEndpointAddresses(ctx context.Context, cluster *v1beta1.Cluster) ([]string, int32, error) {
	url, err := server.URL(ctx, c.Client, cluster, findNonLoopbackSAN(cluster.Spec.TLSSANs))
	if err != nil {
		return nil, 0, err
	}

	port, err := strconv.Atoi(cmp.Or(url.Port(), "443"))
	if err != nil {
		return nil, 0, err
	}

	addr, err := hcpEndpointAddress(ctx, url.Hostname())
	if err != nil {
		return nil, 0, err
	}

	// An Ingress or a LoadBalancer is a single front door that cannot address an
	// individual server, and the port reported for those is not what the nodes listen
	// on, so the per-node addressing only applies to NodePort.
	if cluster.Spec.Expose != nil && cluster.Spec.Expose.NodePort != nil {
		nodeIPs, err := c.serverNodeIPs(ctx, cluster, isIPv4(addr.IP))
		if err != nil {
			return nil, 0, err
		}

		if len(nodeIPs) > 0 {
			return nodeIPs, int32(port), nil
		}
	}

	return []string{addr.IP}, int32(port), nil
}

// isIPv4 reports whether the given address is an IPv4 one. A non-IP address is
// not IPv4.
func isIPv4(address string) bool {
	ip := net.ParseIP(address)

	return ip != nil && ip.To4() != nil
}

// serverNodeIPs returns the sorted internal IPs of the host cluster nodes
// running the server pods of the given cluster, restricted to the IPv4 or IPv6 family.
// Nodes that cannot be fetched are skipped, so a single missing node does not drop the endpoints of all the others.
func (c *Reconciler) serverNodeIPs(ctx context.Context, cluster *v1beta1.Cluster, wantIPv4 bool) ([]string, error) {
	log := ctrl.LoggerFrom(ctx)

	serverPods, err := c.listServerPods(ctx, cluster)
	if err != nil {
		return nil, err
	}

	nodeNames := sets.New[string]()

	for _, pod := range serverPods {
		if pod.Spec.NodeName != "" {
			nodeNames.Insert(pod.Spec.NodeName)
		}
	}

	var ips []string

	// sets.List returns the node names sorted, keeping the endpoints stable across reconciles.
	for _, nodeName := range sets.List(nodeNames) {
		var node corev1.Node
		if err := c.Client.Get(ctx, client.ObjectKey{Name: nodeName}, &node); err != nil {
			log.V(1).Info("skipping node while collecting HCP endpoint addresses", "node", nodeName, "error", err)
			continue
		}

		for _, address := range node.Status.Addresses {
			if address.Type != corev1.NodeInternalIP {
				continue
			}

			if net.ParseIP(address.Address) == nil {
				continue
			}

			if isIPv4(address.Address) != wantIPv4 {
				continue
			}

			ips = append(ips, address.Address)
		}
	}

	return ips, nil
}

// listServerPods returns the host cluster pods running the servers of the given cluster.
func (c *Reconciler) listServerPods(ctx context.Context, cluster *v1beta1.Cluster) ([]corev1.Pod, error) {
	listOpts := []client.ListOption{
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels{
			"cluster": cluster.Name,
			"role":    "server",
		},
	}

	var podList corev1.PodList
	if err := c.Client.List(ctx, &podList, listOpts...); err != nil {
		return nil, fmt.Errorf("listing server pods: %w", err)
	}

	return podList.Items, nil
}

func (c *Reconciler) ensureHCPKubernetesEndpoints(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	ips, port, err := c.hcpEndpointAddresses(ctx, cluster)
	if err != nil {
		return err
	}

	virtClient, err := newVirtualClient(ctx, c.Client, cluster.Name, cluster.Namespace)
	if err != nil {
		return fmt.Errorf("creating virtual cluster client: %w", err)
	}

	//nolint:staticcheck // SA1019 corev1.Endpoints is deprecated in v1.33+, but needed in the Conformance tests
	// We are already using the discoveryv1.EndpointSlice
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: metav1.NamespaceDefault,
		},
	}

	//nolint:staticcheck // SA1019 corev1.EndpointAddress is deprecated in v1.33+, but needed in the Conformance tests
	addresses := make([]corev1.EndpointAddress, 0, len(ips))
	for _, ip := range ips {
		addresses = append(addresses, corev1.EndpointAddress{IP: ip})
	}

	_, err = controllerutil.CreateOrUpdate(ctx, virtClient, endpoints, func() error {
		if endpoints.Labels == nil {
			endpoints.Labels = make(map[string]string)
		}

		// Ensure the skip-mirror label is set
		endpoints.Labels[discoveryv1.LabelSkipMirror] = "true"

		//nolint:staticcheck // SA1019 corev1.EndpointSubset is deprecated in v1.33+, but needed in the Conformance tests
		endpoints.Subsets = []corev1.EndpointSubset{
			{
				Addresses: addresses,
				Ports: []corev1.EndpointPort{
					{
						Name:     "https",
						Port:     port,
						Protocol: corev1.ProtocolTCP,
					},
				},
			},
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("upserting default/kubernetes endpoints in virtual cluster: %w", err)
	}

	log.V(1).Info("HCP kubernetes endpoints reconciled", "ips", ips, "port", port)

	return nil
}

// hcpEndpointAddress builds a corev1.EndpointAddress from the externally
// reachable host. Endpoints require an IP; if the host is a DNS name we
// resolve it. The Hostname field is intentionally left unset:
// the kubernetes API validates it as a DNS-1123 label (no dots),
// so an FQDN like "host.example.com" would be rejected.
func hcpEndpointAddress(ctx context.Context, host string) (corev1.EndpointAddress, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return corev1.EndpointAddress{}, fmt.Errorf("HCP endpoint host %q is a loopback address and cannot be used", host)
		}

		return corev1.EndpointAddress{IP: host}, nil
	}

	ipAddrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return corev1.EndpointAddress{}, fmt.Errorf("HCP endpoint host %q is not an IP and does not resolve: %w", host, err)
	}

	var filteredIPs []net.IP

	for _, addr := range ipAddrs {
		if !addr.IP.IsLoopback() {
			filteredIPs = append(filteredIPs, addr.IP)
		}
	}

	if len(filteredIPs) == 0 {
		return corev1.EndpointAddress{}, fmt.Errorf("HCP endpoint host %q resolved to no non-loopback IPs", host)
	}

	if v4 := filteredIPs[0].To4(); v4 != nil {
		return corev1.EndpointAddress{IP: v4.String()}, nil
	}

	return corev1.EndpointAddress{IP: filteredIPs[0].String()}, nil
}
