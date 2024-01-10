package kubeconfig

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	"github.com/rancher/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type KubeConfig struct {
	AltNames   certutil.AltNames
	CN         string
	ORG        []string
	ExpiryDate time.Duration
}

func (k *KubeConfig) Extract(ctx context.Context, client client.Client, cluster *v1alpha1.Cluster, hostServerIP string) ([]byte, error) {
	nn := types.NamespacedName{
		Name:      cluster.Name + "-bootstrap",
		Namespace: util.ClusterNamespace(cluster),
	}

	var bootstrapSecret v1.Secret
	if err := client.Get(ctx, nn, &bootstrapSecret); err != nil {
		return nil, err
	}

	bootstrapData := bootstrapSecret.Data["bootstrap"]
	if bootstrapData == nil {
		return nil, errors.New("empty bootstrap")
	}

	var bootstrap bootstrap.ControlRuntimeBootstrap
	if err := json.Unmarshal(bootstrapData, &bootstrap); err != nil {
		return nil, err
	}

	adminCert, adminKey, err := CreateClientCertKey(
		k.CN, k.ORG,
		&k.AltNames, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, k.ExpiryDate,
		bootstrap.ClientCA.Content,
		bootstrap.ClientCAKey.Content)
	if err != nil {
		return nil, err
	}
	// get the server service to extract the right IP
	nn = types.NamespacedName{
		Name:      "k3k-server-service",
		Namespace: util.ClusterNamespace(cluster),
	}

	var k3kService v1.Service
	if err := client.Get(ctx, nn, &k3kService); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s:%d", k3kService.Spec.ClusterIP, util.ServerPort)
	if k3kService.Spec.Type == v1.ServiceTypeNodePort {
		nodePort := k3kService.Spec.Ports[0].NodePort
		url = fmt.Sprintf("https://%s:%d", hostServerIP, nodePort)
	}
	kubeconfigData, err := kubeconfig(url, []byte(bootstrap.ServerCA.Content), adminCert, adminKey)
	if err != nil {
		return nil, err
	}

	return kubeconfigData, nil
}

func kubeconfig(url string, serverCA, clientCert, clientKey []byte) ([]byte, error) {
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

	kubeconfig, err := clientcmd.Write(*config)
	if err != nil {
		return nil, err
	}

	return kubeconfig, nil
}
