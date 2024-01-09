package util

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	namespacePrefix = "k3k-"
	k3SImageName    = "rancher/k3s"
	AdminCommonName = "system:admin"
	port            = 6443
)

const (
	K3kSystemNamespace = namespacePrefix + "system"
)

type KubeConfig struct {
	AltNames   certutil.AltNames
	CN         string
	ORG        []string
	ExpiryDate time.Duration
}

func ClusterNamespace(cluster *v1alpha1.Cluster) string {
	return namespacePrefix + cluster.Name
}

func K3SImage(cluster *v1alpha1.Cluster) string {
	return k3SImageName + ":" + cluster.Spec.Version
}

func LogAndReturnErr(errString string, err error) error {
	klog.Errorf("%s: %v", errString, err)
	return err
}

func nodeAddress(node *v1.Node) string {
	var externalIP string
	var internalIP string

	for _, ip := range node.Status.Addresses {
		if ip.Type == "ExternalIP" && ip.Address != "" {
			externalIP = ip.Address
			break
		}
		if ip.Type == "InternalIP" && ip.Address != "" {
			internalIP = ip.Address
		}
	}
	if externalIP != "" {
		return externalIP
	}

	return internalIP
}

// return all the nodes external addresses, if not found then return internal addresses
func Addresses(ctx context.Context, client client.Client) ([]string, error) {
	var nodeList v1.NodeList
	if err := client.List(ctx, &nodeList); err != nil {
		return nil, err
	}

	var addresses []string
	for _, node := range nodeList.Items {
		addresses = append(addresses, nodeAddress(&node))
	}

	return addresses, nil
}

func ExtractKubeconfig(ctx context.Context, client client.Client, cluster *v1alpha1.Cluster, hostServerIP string, cfg *KubeConfig) ([]byte, error) {
	nn := types.NamespacedName{
		Name:      cluster.Name + "-bootstrap",
		Namespace: ClusterNamespace(cluster),
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
		cfg.CN, cfg.ORG,
		&cfg.AltNames, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, cfg.ExpiryDate,
		bootstrap.ClientCA.Content,
		bootstrap.ClientCAKey.Content)
	if err != nil {
		return nil, err
	}
	// get the server service to extract the right IP
	nn = types.NamespacedName{
		Name:      "k3k-server-service",
		Namespace: ClusterNamespace(cluster),
	}

	var k3kService v1.Service
	if err := client.Get(ctx, nn, &k3kService); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s:%d", k3kService.Spec.ClusterIP, port)
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

func CreateClientCertKey(commonName string, organization []string, altNames *certutil.AltNames, extKeyUsage []x509.ExtKeyUsage, expiresAt time.Duration, caCert, caKey string) ([]byte, []byte, error) {
	caKeyPEM, err := certutil.ParsePrivateKeyPEM([]byte(caKey))
	if err != nil {
		return nil, nil, err
	}

	caCertPEM, err := certutil.ParseCertsPEM([]byte(caCert))
	if err != nil {
		return nil, nil, err
	}

	b, err := generateKey()
	if err != nil {
		return nil, nil, err
	}

	key, err := certutil.ParsePrivateKeyPEM(b)
	if err != nil {
		return nil, nil, err
	}

	cfg := certutil.Config{
		CommonName:   commonName,
		Organization: organization,
		Usages:       extKeyUsage,
		ExpiresAt:    expiresAt,
	}
	if altNames != nil {
		cfg.AltNames = *altNames
	}
	cert, err := certutil.NewSignedCert(cfg, key.(crypto.Signer), caCertPEM[0], caKeyPEM.(crypto.Signer))
	if err != nil {
		return nil, nil, err
	}

	return append(certutil.EncodeCertPEM(cert), certutil.EncodeCertPEM(caCertPEM[0])...), b, nil
}

func generateKey() (data []byte, err error) {
	generatedData, err := certutil.MakeEllipticPrivateKeyPEM()
	if err != nil {
		return nil, fmt.Errorf("error generating key: %v", err)
	}

	return generatedData, nil
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

func AddSANs(sans []string) certutil.AltNames {
	var altNames certutil.AltNames
	for _, san := range sans {
		ip := net.ParseIP(san)
		if ip == nil {
			altNames.DNSNames = append(altNames.DNSNames, san)
		} else {
			altNames.IPs = append(altNames.IPs, ip)
		}
	}
	return altNames
}
