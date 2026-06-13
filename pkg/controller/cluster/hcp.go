package cluster

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

// ensureHCPRegistration computes the K3s installer command external nodes can
// run to join an HCP-mode cluster and stores it on cluster.Status.HCPRegistration.
//
// When the cluster's Service is not externally reachable (no NodePort,
// LoadBalancer or Ingress configured) the command cannot be built; in that
// case the Ready condition is set to False with reason HCPNoExternalEndpoint
// so the operator surfaces the problem without failing the reconciliation.
func (c *ClusterReconciler) ensureHCPRegistration(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	_, external, err := server.ServerURL(ctx, c.Client, cluster, selectNonLoopbackSAN(cluster), 0)
	if err != nil {
		return err
	}

	if !external {
		log.Info("HCP cluster has no externally-routable endpoint; skipping registration command",
			"cluster", cluster.Name, "namespace", cluster.Namespace)

		meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:    ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonHCPNoExternalEndpoint,
			Message: "HCP cluster has no external endpoint; set spec.expose.nodePort, spec.expose.loadBalancer or spec.expose.ingress so external nodes can reach the API server",
		})
	}

	return nil
}

// selectNonLoopbackSAN returns the first non-loopback address from the
// cluster's TLS SANs, preferring spec.TLSSANs then falling back to status.TLSSANs.
// Returns empty string if no non-loopback address is found.
func selectNonLoopbackSAN(cluster *v1beta1.Cluster) string {
	// Try spec.TLSSANs first (user-provided values)
	for _, san := range cluster.Spec.TLSSANs {
		if ip := net.ParseIP(san); ip != nil && ip.IsLoopback() {
			continue
		}

		if san == "localhost" {
			continue
		}

		return san // Found a non-loopback address
	}

	// Fall back to status.TLSSANs (computed values)
	for _, san := range cluster.Status.TLSSANs {
		if ip := net.ParseIP(san); ip != nil && ip.IsLoopback() {
			continue
		}

		if san == "localhost" {
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
func (c *ClusterReconciler) ensureHCPKubernetesEndpointSlice(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	rawURL, external, err := server.ServerURL(ctx, c.Client, cluster, selectNonLoopbackSAN(cluster), 0)
	if err != nil {
		return err
	}

	if !external {
		// ensureHCPRegistration already surfaces this via Ready=False;
		// nothing for us to do here.
		return nil
	}

	host, port, err := parseHCPHostPort(rawURL)
	if err != nil {
		return fmt.Errorf("parsing HCP server URL %q: %w", rawURL, err)
	}

	addr, err := hcpEndpointAddress(host)
	if err != nil {
		return err
	}

	var addressType discoveryv1.AddressType

	if ip := net.ParseIP(addr.IP); ip != nil {
		if ip.To4() != nil {
			addressType = discoveryv1.AddressTypeIPv4
		} else {
			addressType = discoveryv1.AddressTypeIPv6
		}
	} else {
		return fmt.Errorf("invalid IP address %q", addr.IP)
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

		endpoint := discoveryv1.Endpoint{
			Addresses: []string{addr.IP},
		}

		if addr.Hostname != "" {
			endpoint.Hostname = &addr.Hostname
		}

		endpointSlice.Endpoints = []discoveryv1.Endpoint{endpoint}
		portName := "https"

		endpointSlice.Ports = []discoveryv1.EndpointPort{
			{
				Name:     &portName,
				Port:     &port,
				Protocol: new(corev1.ProtocolTCP),
			},
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("upserting default/kubernetes endpointslice in virtual cluster: %w", err)
	}

	log.V(1).Info("HCP kubernetes endpointslice reconciled",
		"address", addr.IP, "hostname", addr.Hostname, "port", port)

	return nil
}

func (c *ClusterReconciler) ensureHCPKubernetesEndpoints(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	rawURL, external, err := server.ServerURL(ctx, c.Client, cluster, selectNonLoopbackSAN(cluster), 0)
	if err != nil {
		return err
	}

	if !external {
		// ensureHCPRegistration already surfaces this via Ready=False;
		// nothing for us to do here.
		return nil
	}

	host, port, err := parseHCPHostPort(rawURL)
	if err != nil {
		return fmt.Errorf("parsing HCP server URL %q: %w", rawURL, err)
	}

	addr, err := hcpEndpointAddress(host)
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

	_, err = controllerutil.CreateOrUpdate(ctx, virtClient, endpoints, func() error {
		if endpoints.Labels == nil {
			endpoints.Labels = make(map[string]string)
		}

		// Ensure the skip-mirror label is set
		endpoints.Labels[discoveryv1.LabelSkipMirror] = "true"

		//nolint:staticcheck // SA1019 corev1.EndpointSubset is deprecated in v1.33+, but needed in the Conformance tests
		endpoints.Subsets = []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{addr},
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

	log.V(1).Info("HCP kubernetes endpoints reconciled", "address", addr.IP, "hostname", addr.Hostname, "port", port)

	return nil
}

// parseHCPHostPort extracts the host and port from a server URL produced by
// server.ServerURL. The port defaults to 443 when omitted.
func parseHCPHostPort(rawURL string) (string, int32, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", 0, err
	}

	host := u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("missing host in URL %q", rawURL)
	}

	portStr := u.Port()

	var port int32 = 443

	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port in URL %q: %w", rawURL, err)
		}

		if p <= 0 || p > 65535 {
			return "", 0, fmt.Errorf("port %d out of range in URL %q", p, rawURL)
		}

		port = int32(p)
	}

	return host, port, nil
}

// hcpEndpointAddress builds a corev1.EndpointAddress from the externally
// reachable host. Endpoints require an IP; if the host is a DNS name we
// resolve it and keep the original name as Hostname so logs/events remain
// human-readable.
func hcpEndpointAddress(host string) (corev1.EndpointAddress, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return corev1.EndpointAddress{}, fmt.Errorf("HCP endpoint host %q is a loopback address and cannot be used", host)
		}

		return corev1.EndpointAddress{IP: host}, nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return corev1.EndpointAddress{}, fmt.Errorf("HCP endpoint host %q is not an IP and does not resolve: %w", host, err)
	}

	var filteredIPs []net.IP

	for _, ip := range ips {
		if !ip.IsLoopback() {
			filteredIPs = append(filteredIPs, ip)
		}
	}

	if len(filteredIPs) == 0 {
		return corev1.EndpointAddress{}, fmt.Errorf("HCP endpoint host %q resolved to no non-loopback IPs", host)
	}

	if v4 := filteredIPs[0].To4(); v4 != nil {
		return corev1.EndpointAddress{IP: v4.String(), Hostname: host}, nil
	}

	return corev1.EndpointAddress{IP: filteredIPs[0].String(), Hostname: host}, nil
}
