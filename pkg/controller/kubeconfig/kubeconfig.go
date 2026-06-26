package kubeconfig

import (
	"context"
	"crypto/x509"
	"time"

	"k8s.io/apiserver/pkg/authentication/user"
	"sigs.k8s.io/controller-runtime/pkg/client"

	certutil "github.com/rancher/dynamiclistener/cert"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

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

	serverURL, err := server.ServerURL(ctx, client, cluster, hostServerIP)
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
