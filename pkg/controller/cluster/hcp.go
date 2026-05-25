package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

// ErrHCPNoExternalEndpoint is returned by ensureHCPRegistration when an
// HCP-mode cluster has no externally-routable endpoint (no NodePort,
// LoadBalancer or Ingress) so external worker nodes cannot reach the API
// server. updateStatus translates it into a Ready=False condition with
// reason HCPNoExternalEndpoint instead of failing the reconcile outright.
var ErrHCPNoExternalEndpoint = errors.New("HCP cluster has no external endpoint")

// ensureHCPRegistration verifies that an HCP-mode cluster exposes an
// externally-routable API server endpoint so external worker nodes can join.
// Join instructions (the `curl ... | sh -` line) are printed by the CLI
// (`k3kcli cluster create` / `k3kcli kubeconfig generate`); the controller
// does not persist them on the Cluster object.
//
// Returns ErrHCPNoExternalEndpoint when no NodePort, LoadBalancer or Ingress
// is configured, which updateStatus surfaces as Ready=False with reason
// HCPNoExternalEndpoint.
func (c *ClusterReconciler) ensureHCPRegistration(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)
	log = log.WithValues("cluster", cluster.Name, "namespace", cluster.Namespace)

	host := selectNonLoopbackSAN(cluster.Spec.TLSSANs)
	if host == "" {
		log.Info("HCP cluster is missing a non-loopback spec.tlsSANs entry for external node registration")

		return ErrHCPNoExternalEndpoint
	}

	_, external, err := server.ServerURL(ctx, c.Client, cluster, host)
	if err != nil {
		return err
	}

	if !external {
		log.Info("HCP cluster has no externally-routable endpoint")

		return ErrHCPNoExternalEndpoint
	}

	return nil
}

// selectNonLoopbackSAN returns the first non-loopback address from the given
// TLS SANs. Returns empty string if none is found.
func selectNonLoopbackSAN(sans []string) string {
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
func (c *ClusterReconciler) ensureHCPKubernetesEndpointSlice(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	rawURL, external, err := server.ServerURL(ctx, c.Client, cluster, selectNonLoopbackSAN(cluster.Spec.TLSSANs))
	if err != nil {
		return err
	}

	if !external {
		// Defensive: reconcile would have already short-circuited with
		// ErrHCPNoExternalEndpoint via ensureHCPRegistration before reaching
		// here, but skip gracefully if invoked directly.
		return nil
	}

	host, port, err := parseHCPHostPort(rawURL.String())
	if err != nil {
		return fmt.Errorf("parsing HCP server URL %q: %w", rawURL, err)
	}

	addr, err := hcpEndpointAddress(ctx, host)
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

		endpointSlice.Endpoints = []discoveryv1.Endpoint{
			{Addresses: []string{addr.IP}},
		}
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

	log.V(1).Info("HCP kubernetes endpointslice reconciled", "address", addr.IP, "host", host, "port", port)

	return nil
}

func (c *ClusterReconciler) ensureHCPKubernetesEndpoints(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	url, external, err := server.ServerURL(ctx, c.Client, cluster, selectNonLoopbackSAN(cluster.Spec.TLSSANs))
	if err != nil {
		return err
	}

	if !external {
		// Defensive: reconcile would have already short-circuited with
		// ErrHCPNoExternalEndpoint via ensureHCPRegistration before reaching
		// here, but skip gracefully if invoked directly.
		return nil
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

	port, err := strconv.Atoi(url.Port())
	if err != nil {
		return err
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
						Port:     int32(port),
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

	log.V(1).Info("HCP kubernetes endpoints reconciled", "address", addr.IP, "host", url.Host, "port", port)

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
