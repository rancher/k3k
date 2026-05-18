package cluster

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

// endpointSliceSkipMirrorLabel is the upstream label that opts an Endpoints
// object out of the kube-controller-manager EndpointSlice mirroring controller.
// The kube-apiserver normally sets it on default/kubernetes (because it
// manages EndpointSlices itself); in HCP mode we want the mirror controller
// to handle slices, so we strip the label.
const endpointSliceSkipMirrorLabel = "endpointslice.kubernetes.io/skip-mirror"

// ensureHCPRegistration computes the K3s installer command external nodes can
// run to join an HCP-mode cluster and stores it on cluster.Status.HCPRegistration.
//
// When the cluster's Service is not externally reachable (no NodePort,
// LoadBalancer or Ingress configured) the command cannot be built; in that
// case the Ready condition is set to False with reason HCPNoExternalEndpoint
// so the operator surfaces the problem without failing the reconciliation.
func (c *ClusterReconciler) ensureHCPRegistration(ctx context.Context, cluster *v1beta1.Cluster, token string) error {
	log := ctrl.LoggerFrom(ctx)

	url, external, err := server.ServerURL(ctx, c.Client, cluster, "", 0)
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

		cluster.Status.HCPRegistration = ""

		return nil
	}

	version := cluster.Spec.Version
	if version == "" {
		version = cluster.Status.HostVersion
	}

	cluster.Status.HCPRegistration = hcpRegistrationCommand(version, url, token)

	return nil
}

// hcpRegistrationCommand returns the standard K3s installer one-liner an
// end-user can copy onto an external host to join an HCP cluster.
func hcpRegistrationCommand(version, serverURL, token string) string {
	if version == "" {
		return fmt.Sprintf("curl -sfL https://get.k3s.io | K3S_URL=%s K3S_TOKEN=%s sh -", serverURL, token)
	}

	return fmt.Sprintf("curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=%s K3S_URL=%s K3S_TOKEN=%s sh -",
		version, serverURL, token)
}

// ensureHCPKubernetesEndpoints maintains the default/kubernetes Service
// Endpoints inside the virtual cluster, pointing them at the externally
// reachable host:port (NodePort / LoadBalancer / Ingress) so that pods
// scheduled on external worker nodes can reach the in-cluster apiserver
// ClusterIP.
//
// Background: the kube-apiserver normally reconciles default/kubernetes
// Endpoints to its own --advertise-address:--secure-port (the host-cluster
// pod IP and 6443). External worker nodes have no route to the host-cluster
// pod CIDR, so kube-proxy DNAT to that endpoint fails. We disable the
// apiserver reconciler in HCP mode (see serverOptions) and own this
// Endpoints object instead.
func (c *ClusterReconciler) ensureHCPKubernetesEndpoints(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	rawURL, external, err := server.ServerURL(ctx, c.Client, cluster, "", 0)
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

	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: metav1.NamespaceDefault,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, virtClient, endpoints, func() error {
		// Allow EndpointSlice mirroring; the apiserver may have set
		// skip-mirror=true before we disabled its endpoint reconciler.
		if endpoints.Labels != nil {
			delete(endpoints.Labels, endpointSliceSkipMirrorLabel)
		}

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

	log.V(1).Info("HCP kubernetes endpoints reconciled",
		"address", addr.IP, "hostname", addr.Hostname, "port", port)

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
		return corev1.EndpointAddress{IP: host}, nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return corev1.EndpointAddress{}, fmt.Errorf("HCP endpoint host %q is not an IP and does not resolve: %w", host, err)
	}

	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return corev1.EndpointAddress{IP: v4.String(), Hostname: host}, nil
		}
	}

	if len(ips) == 0 {
		return corev1.EndpointAddress{}, fmt.Errorf("HCP endpoint host %q resolved to no IPs", host)
	}

	return corev1.EndpointAddress{IP: ips[0].String(), Hostname: host}, nil
}
