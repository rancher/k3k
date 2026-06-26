package kubeconfig

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/user"
	"sigs.k8s.io/controller-runtime/pkg/client"

	certutil "github.com/rancher/dynamiclistener/cert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
)

type KubeConfig struct {
	AltNames   certutil.AltNames
	CN         string
	ORG        []string
	ExpiryDate time.Duration
}

func New() *KubeConfig {
	return &KubeConfig{
		CN:         controller.AdminCommonName,
		ORG:        []string{user.SystemPrivilegedGroup},
		ExpiryDate: 0,
	}
}

func (k *KubeConfig) Generate(ctx context.Context, client client.Client, cluster *v1beta1.Cluster, hostServerIP string) (*clientcmdapi.Config, error) {
	bootstrapData, err := bootstrap.LoadFromSecret(ctx, client, cluster)
	if err != nil {
		return nil, err
	}

	serverCACert := []byte(bootstrapData.ServerCA.Content)

	adminCert, adminKey, err := certs.CreateClientCertKey(
		k.CN,
		k.ORG,
		&k.AltNames,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		k.ExpiryDate,
		bootstrapData.ClientCA.Content,
		bootstrapData.ClientCAKey.Content,
	)
	if err != nil {
		return nil, err
	}

	serverURL, err := getURLFromService(ctx, client, cluster, hostServerIP)
	if err != nil {
		return nil, err
	}

	config := NewConfig(serverURL.String(), serverCACert, adminCert, adminKey)

	return config, nil
}

func NewConfig(url string, serverCA, clientCert, clientKey []byte) *clientcmdapi.Config {
	config := clientcmdapi.NewConfig()

	cluster := clientcmdapi.NewCluster()
	cluster.CertificateAuthorityData = serverCA
	cluster.Server = url

	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.ClientCertificateData = clientCert
	authInfo.ClientKeyData = clientKey

	context := clientcmdapi.NewContext()
	context.AuthInfo = "default"
	context.Cluster = "default"

	config.Clusters["default"] = cluster
	config.AuthInfos["default"] = authInfo
	config.Contexts["default"] = context
	config.CurrentContext = "default"

	return config
}

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
func getURLFromService(ctx context.Context, c client.Client, cluster *v1beta1.Cluster, hostServerIP string) (*url.URL, error) {
	log := ctrl.LoggerFrom(ctx)

	key := types.NamespacedName{
		Name:      server.ServiceName(cluster.Name),
		Namespace: cluster.Namespace,
	}

	// Check if ingress is configured
	if cluster.Spec.Expose != nil && cluster.Spec.Expose.Ingress != nil {
		key := types.NamespacedName{
			Name:      server.IngressName(cluster.Name),
			Namespace: cluster.Namespace,
		}

		var k3kIngress networkingv1.Ingress
		if err := c.Get(ctx, key, &k3kIngress); err != nil {
			return nil, err
		}

		if len(k3kIngress.Spec.Rules) > 0 && k3kIngress.Spec.Rules[0].Host != "" {
			return url.Parse(fmt.Sprintf("https://%s", k3kIngress.Spec.Rules[0].Host))
		}

		log.V(1).Info("Ingress has no rule with a host set, falling back to the service URL.")
	}

	// Fall back to Service-based URL
	var k3kService corev1.Service
	if err := c.Get(ctx, key, &k3kService); err != nil {
		return nil, err
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

	return url.Parse(rawURL)
}
