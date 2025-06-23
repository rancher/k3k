package kubeconfig

import (
	"context"
	"crypto/x509"
	"fmt"
	"slices"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/user"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (k *KubeConfig) Generate(ctx context.Context, client client.Client, cluster *v1alpha1.Cluster, hostServerIP string) (*clientcmdapi.Config, error) {
	bootstrapData, err := bootstrap.GetFromSecret(ctx, client, cluster)
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

	url, err := getURLFromService(ctx, client, cluster, hostServerIP)
	if err != nil {
		return nil, err
	}

	config := NewConfig(url, serverCACert, adminCert, adminKey)

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

func getURLFromService(ctx context.Context, client client.Client, cluster *v1alpha1.Cluster, hostServerIP string) (string, error) {
	// get the server service to extract the right IP
	key := types.NamespacedName{
		Name:      server.ServiceName(cluster.Name),
		Namespace: cluster.Namespace,
	}

	var k3kService v1.Service
	if err := client.Get(ctx, key, &k3kService); err != nil {
		return "", err
	}

	ip := k3kService.Spec.ClusterIP
	port := int32(443)

	switch k3kService.Spec.Type {
	case v1.ServiceTypeNodePort:
		ip = hostServerIP
		port = k3kService.Spec.Ports[0].NodePort
	case v1.ServiceTypeLoadBalancer:
		ip = k3kService.Status.LoadBalancer.Ingress[0].IP
		port = k3kService.Spec.Ports[0].Port
	}

	if !slices.Contains(cluster.Status.TLSSANs, ip) {
		logrus.Warnf("ip %s not in tlsSANs", ip)

		if len(cluster.Spec.TLSSANs) > 0 {
			logrus.Warnf("Using the first TLS SAN in the spec as a fallback: %s", cluster.Spec.TLSSANs[0])

			ip = cluster.Spec.TLSSANs[0]
		} else if len(cluster.Status.TLSSANs) > 0 {
			logrus.Warnf("No explicit tlsSANs specified. Trying to use the first TLS SAN in the status: %s", cluster.Status.TLSSANs[0])

			ip = cluster.Status.TLSSANs[0]
		} else {
			logrus.Warn("ip not found in tlsSANs. This could cause issue with the certificate validation.")
		}
	}

	url := "https://" + ip
	if port != 443 {
		url = fmt.Sprintf("%s:%d", url, port)
	}

	// if ingress is specified, use the ingress host
	if cluster.Spec.Expose != nil && cluster.Spec.Expose.Ingress != nil {
		var k3kIngress networkingv1.Ingress

		ingressKey := types.NamespacedName{
			Name:      server.IngressName(cluster.Name),
			Namespace: cluster.Namespace,
		}

		if err := client.Get(ctx, ingressKey, &k3kIngress); err != nil {
			return "", err
		}

		url = fmt.Sprintf("https://%s", k3kIngress.Spec.Rules[0].Host)
	}

	return url, nil
}
