package cluster

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"strconv"

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
// reachable host:port (NodePort / LoadBalancer / Ingress) so that pods
// scheduled on external worker nodes can reach the in-cluster apiserver
// ClusterIP.
//
// Background: the kube-apiserver normally reconciles default/kubernetes
// EndpointSlice to its own --advertise-address:--secure-port (the host-cluster
// pod IP and 6443). External worker nodes have no route to the host-cluster
// pod CIDR, so kube-proxy DNAT to that endpoint fails. We disable the
// apiserver reconciler in HCP mode (see serverOptions) and own this
// EndpointSlice object instead.
func (c *Reconciler) ensureHCPKubernetesEndpointSlice(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	url, err := server.URL(ctx, c.Client, cluster, findNonLoopbackSAN(cluster.Spec.TLSSANs))
	if err != nil {
		return err
	}

	portStr := cmp.Or(url.Port(), "443")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return err
	}

	addr, err := hcpEndpointAddress(ctx, url.Hostname())
	if err != nil {
		return err
	}

	isIPv4 := true
	if ip := net.ParseIP(addr.IP); ip != nil && ip.To4() == nil {
		isIPv4 = false
	}

	var ips []string

	listOpts := []client.ListOptions{
		{client.InNamespace(cluster.Namespace)},
		client.MatchingLabels{
			"cluster": cluster.Name,
			"role":    "server",
		},
	}

	var podList corev1.PodList
	if err := c.Client.List(ctx, &podList, listOpts); err == nil && len(podList.Items) > 0 {
		nodeNames := make(map[string]bool)

		for _, pod := range podList.Items {
			if pod.Spec.NodeName != "" {
				nodeNames[pod.Spec.NodeName] = true
			}
		}

		for nodeName := range nodeNames {
			var node corev1.Node
			if err := c.Client.Get(ctx, client.ObjectKey{Name: nodeName}, &node); err == nil {
				for _, address := range node.Status.Addresses {
					if address.Type == corev1.NodeInternalIP {
						nodeIP := net.ParseIP(address.Address)
						if nodeIP != nil {
							if isIPv4 && nodeIP.To4() != nil {
								ips = append(ips, address.Address)
							} else if !isIPv4 && nodeIP.To4() == nil {
								ips = append(ips, address.Address)
							}
						}
					}
				}
			}
		}
	}

	if len(ips) == 0 {
		ips = append(ips, addr.IP)
	}

	var addressType discoveryv1.AddressType
	if isIPv4 {
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
				Port:     new(int32(port)),
				Protocol: new(corev1.ProtocolTCP),
			},
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("upserting default/kubernetes endpointslice in virtual cluster: %w", err)
	}

	log.V(1).Info("HCP kubernetes endpointslice reconciled", "ips", ips, "host", url.Hostname(), "port", port)

	return nil
}

func (c *Reconciler) ensureHCPKubernetesEndpoints(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	url, err := server.URL(ctx, c.Client, cluster, findNonLoopbackSAN(cluster.Spec.TLSSANs))
	if err != nil {
		return err
	}

	addr, err := hcpEndpointAddress(ctx, url.Hostname())
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

	portStr := cmp.Or(url.Port(), "443")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return err
	}

	var addresses []corev1.EndpointAddress

	epPort := int32(port)

	var podList corev1.PodList
	if err := c.Client.List(ctx, &podList, client.InNamespace(cluster.Namespace), client.MatchingLabels{
		"cluster": cluster.Name,
		"role":    "server",
	}); err == nil && len(podList.Items) > 0 {
		for _, pod := range podList.Items {
			if pod.Status.PodIP != "" {
				addresses = append(addresses, corev1.EndpointAddress{
					IP: pod.Status.PodIP,
					TargetRef: &corev1.ObjectReference{
						Kind:            "Pod",
						Name:            pod.Name,
						Namespace:       pod.Namespace,
						UID:             pod.UID,
						ResourceVersion: pod.ResourceVersion,
					},
				})
			}
		}
	}

	if len(addresses) > 0 {
		// If using direct pod IPs, they listen on 6443
		epPort = 6443
	} else {
		addresses = append(addresses, addr)
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
						Port:     epPort,
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

	log.V(1).Info("HCP kubernetes endpoints reconciled", "addresses", addresses, "port", epPort)

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
