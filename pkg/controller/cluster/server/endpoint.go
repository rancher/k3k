package server

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// ServerURL returns the URL at which the K3s API server of a virtual cluster
// is reachable. The second return value reports whether that URL is routable
// from outside the host cluster (true for NodePort/LoadBalancer/Ingress, false
// for plain ClusterIP exposition).
//
// hostServerIP is used as the address when the underlying Service is a
// NodePort. serverPort, when non-zero, overrides the port discovered from the
// Service.
// --------------------------------------
// getURLFromService generates the API server URL for the kubeconfig based on the service configuration.
//
// It handles internal vs external access patterns:
//   - Internal access (hostServerIP == service.ClusterIP): uses the ClusterIP for direct pod-to-pod communication
//   - External access (hostServerIP != service.ClusterIP): uses the appropriate external endpoint based on the service type
//
// Service type handling:
//   - ClusterIP: uses service.Spec.ClusterIP (internal-only)
//   - NodePort: uses hostServerIP:NodePort for external access, ClusterIP:Port for internal access
//   - LoadBalancer: uses the LoadBalancer ingress IP, falling back to its hostname
//   - Ingress (if configured): takes precedence over the service-based URL
//
// The hostServerIP parameter determines the access pattern:
//   - Controller reconciliation: passes service.Spec.ClusterIP → internal access
//   - CLI kubeconfig export: passes the external host → external access
func ServerURL(ctx context.Context, c client.Client, cluster *v1beta1.Cluster, hostServerIP string) (*url.URL, bool, error) {
	log := ctrl.LoggerFrom(ctx)

	key := types.NamespacedName{
		Name:      ServiceName(cluster.Name),
		Namespace: cluster.Namespace,
	}

	// Check if ingress is configured
	if cluster.Spec.Expose != nil && cluster.Spec.Expose.Ingress != nil {
		key := types.NamespacedName{
			Name:      IngressName(cluster.Name),
			Namespace: cluster.Namespace,
		}

		var k3kIngress networkingv1.Ingress
		if err := c.Get(ctx, key, &k3kIngress); err != nil {
			return nil, false, err
		}

		if len(k3kIngress.Spec.Rules) > 0 && k3kIngress.Spec.Rules[0].Host != "" {
			u, err := url.Parse(fmt.Sprintf("https://%s", k3kIngress.Spec.Rules[0].Host))
			return u, false, err
		}

		log.V(1).Info("Ingress has no rule with a host set, falling back to the service URL.")
	}

	// Fall back to Service-based URL
	var k3kService corev1.Service
	if err := c.Get(ctx, key, &k3kService); err != nil {
		return nil, false, err
	}

	// init to hostServerIP and 443 port
	ip := hostServerIP
	port := int32(443)

	// Use service port as default if available
	if len(k3kService.Spec.Ports) > 0 {
		port = k3kService.Spec.Ports[0].Port
	}

	// Handle each service type separately
	switch k3kService.Spec.Type {
	case corev1.ServiceTypeClusterIP:
		ip = k3kService.Spec.ClusterIP

	case corev1.ServiceTypeNodePort:
		// Only use NodePort if hostServerIP is NOT the ClusterIP
		// If hostServerIP == ClusterIP, this is an internal connection, use ClusterIP
		if hostServerIP != k3kService.Spec.ClusterIP {
			if len(k3kService.Spec.Ports) > 0 {
				port = k3kService.Spec.Ports[0].NodePort
			}
		} else {
			// Internal connection: use ClusterIP
			ip = k3kService.Spec.ClusterIP
		}

	case corev1.ServiceTypeLoadBalancer:
		if len(k3kService.Status.LoadBalancer.Ingress) > 0 {
			ingress := k3kService.Status.LoadBalancer.Ingress[0]

			switch {
			case ingress.IP != "":
				ip = ingress.IP
			case ingress.Hostname != "":
				ip = ingress.Hostname
			default:
				log.V(1).Info("No usable ingress address found in LoadBalancer service.")
			}
		}
	}

	if !slices.Contains(cluster.Status.TLSSANs, ip) {
		log.V(1).Info(fmt.Sprintf("IP %s not in tlsSANs.", ip))

		if len(cluster.Spec.TLSSANs) > 0 {
			log.V(1).Info("Using the first TLS SAN in the spec as a fallback: " + cluster.Spec.TLSSANs[0])

			ip = cluster.Spec.TLSSANs[0]
		} else if len(cluster.Status.TLSSANs) > 0 {
			log.V(1).Info("No explicit tlsSANs specified. Trying to use the first TLS SAN in the status: " + cluster.Status.TLSSANs[0])

			ip = cluster.Status.TLSSANs[0]
		} else {
			log.V(1).Info("IP not found in tlsSANs. This could cause issue with the certificate validation.")
		}
	}

	// Build URL with port only if not the default HTTPS port
	var rawURL string
	if port != int32(443) {
		rawURL = fmt.Sprintf("https://%s", net.JoinHostPort(ip, strconv.Itoa(int(port))))
	} else {
		rawURL = fmt.Sprintf("https://%s", ip)
	}

	u, err := url.Parse(rawURL)

	return u, false, err
}
