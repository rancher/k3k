package server

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/retry"
)

const (
	adminCommonName = "system:admin"
	port            = 6443
)

type controlRuntimeBootstrap struct {
	ServerCA    content
	ServerCAKey content
	ClientCA    content
	ClientCAKey content
}

type content struct {
	Timestamp string
	Content   string
}

// GenerateNewKubeConfig generates the kubeconfig for the cluster:
// 1- use the server token to get the bootstrap data from k3s
// 2- generate client admin cert/key
// 3- use the ca cert from the bootstrap data & admin cert/key to write a new kubeconfig
// 4- save the new kubeconfig as a secret
func (s *Server) GenerateNewKubeConfig(ctx context.Context, ip string) (*v1.Secret, error) {
	token := s.cluster.Spec.Token

	var bootstrap *controlRuntimeBootstrap
	if err := retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return true
	}, func() error {
		var err error
		bootstrap, err = requestBootstrap(token, ip)
		return err
	}); err != nil {
		return nil, err
	}

	if err := decodeBootstrap(bootstrap); err != nil {
		return nil, err
	}

	adminCert, adminKey, err := createClientCertKey(
		adminCommonName, []string{user.SystemPrivilegedGroup},
		nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		bootstrap.ClientCA.Content,
		bootstrap.ClientCAKey.Content)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s:%d", ip, port)
	kubeconfigData, err := kubeconfig(url, []byte(bootstrap.ServerCA.Content), adminCert, adminKey)
	if err != nil {
		return nil, err
	}

	return &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.cluster.Name + "-kubeconfig",
			Namespace: util.ClusterNamespace(s.cluster),
		},
		Data: map[string][]byte{
			"kubeconfig.yaml": kubeconfigData,
		},
	}, nil

}

func requestBootstrap(token, serverIP string) (*controlRuntimeBootstrap, error) {
	url := "https://" + serverIP + ":6443/v1-k3s/server-bootstrap"

	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Basic "+basicAuth("server", token))

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var runtimeBootstrap controlRuntimeBootstrap
	if err := json.NewDecoder(resp.Body).Decode(&runtimeBootstrap); err != nil {
		return nil, err
	}

	return &runtimeBootstrap, nil
}

func createClientCertKey(commonName string, organization []string, altNames *certutil.AltNames, extKeyUsage []x509.ExtKeyUsage, caCert, caKey string) ([]byte, []byte, error) {
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

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func decodeBootstrap(bootstrap *controlRuntimeBootstrap) error {
	//client-ca
	decoded, err := base64.StdEncoding.DecodeString(bootstrap.ClientCA.Content)
	if err != nil {
		return err
	}
	bootstrap.ClientCA.Content = string(decoded)

	//client-ca-key
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ClientCAKey.Content)
	if err != nil {
		return err
	}
	bootstrap.ClientCAKey.Content = string(decoded)

	//server-ca
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ServerCA.Content)
	if err != nil {
		return err
	}
	bootstrap.ServerCA.Content = string(decoded)

	//server-ca-key
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ServerCAKey.Content)
	if err != nil {
		return err
	}
	bootstrap.ServerCAKey.Content = string(decoded)

	return nil
}
