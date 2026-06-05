package k3s

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/controller"
)

const (
	TLSDir = "/var/lib/rancher/k3s/server/tls/"
)

type BootstrapData struct {
	ServerCA        cert `json:"serverCA"`
	ServerCAKey     cert `json:"serverCAKey"`
	ClientCA        cert `json:"clientCA"`
	ClientCAKey     cert `json:"clientCAKey"`
	ETCDServerCA    cert `json:"etcdServerCA"`
	ETCDServerCAKey cert `json:"etcdServerCAKey"`
}

type cert struct {
	Timestamp string
	Content   string
}

type K3SConfig struct {
	ClusterInit bool `json:"ClusterInit"`
}

func GetServerConfig(c *Client) (*K3SConfig, error) {
	endpoint := "/v1-k3s/config"

	k3sConfig, err := do[K3SConfig](c, endpoint, "node", http.MethodGet)
	if err != nil {
		return nil, err
	}

	return k3sConfig, nil
}

func GetServerBootstrap(c *Client) (*BootstrapData, error) {
	endpoint := "/v1-k3s/server-bootstrap"

	bootstrap, err := do[BootstrapData](c, endpoint, "server", http.MethodGet)
	if err != nil {
		return nil, err
	}

	// we still need to decode each certs since the bootstrap data endpoint base64 encode each cert
	if err := decode(bootstrap); err != nil {
		return nil, fmt.Errorf("failed to decode bootstrap secret: %w", err)
	}

	return bootstrap, nil
}

func decode(data *BootstrapData) error {
	// client-ca
	decoded, err := base64.StdEncoding.DecodeString(data.ClientCA.Content)
	if err != nil {
		return err
	}

	data.ClientCA.Content = string(decoded)

	// client-ca-key
	decoded, err = base64.StdEncoding.DecodeString(data.ClientCAKey.Content)
	if err != nil {
		return err
	}

	data.ClientCAKey.Content = string(decoded)

	// server-ca
	decoded, err = base64.StdEncoding.DecodeString(data.ServerCA.Content)
	if err != nil {
		return err
	}

	data.ServerCA.Content = string(decoded)

	// server-ca-key
	decoded, err = base64.StdEncoding.DecodeString(data.ServerCAKey.Content)
	if err != nil {
		return err
	}

	data.ServerCAKey.Content = string(decoded)

	// etcd-ca
	decoded, err = base64.StdEncoding.DecodeString(data.ETCDServerCA.Content)
	if err != nil {
		return err
	}

	data.ETCDServerCA.Content = string(decoded)

	// etcd-ca-key
	decoded, err = base64.StdEncoding.DecodeString(data.ETCDServerCAKey.Content)
	if err != nil {
		return err
	}

	data.ETCDServerCAKey.Content = string(decoded)

	return nil
}

func ReadBootstrapFromK3sPod(ctx context.Context, restConfig *rest.Config, clusterName, clusterNamespace string) (*BootstrapData, error) {
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	// using the first server in the statefulset to get the bootstrap data
	serverPodName := controller.SafeConcatNameWithPrefix(clusterName, "server-0")

	// skipping etcd since reading from pod only required with external datastore
	bootstrapCerts := map[string]string{
		"server-ca.crt": "",
		"server-ca.key": "",
		"client-ca.crt": "",
		"client-ca.key": "",
	}
	for certName := range bootstrapCerts {
		command := []string{"cat", filepath.Join(TLSDir, certName)}

		certData, err := podExec(ctx, clientset, restConfig, clusterNamespace, serverPodName, command)
		if err != nil {
			return nil, err
		}

		bootstrapCerts[certName] = string(certData)
	}

	bootstrap := &BootstrapData{
		ServerCA:    cert{Content: bootstrapCerts["server-ca.crt"]},
		ServerCAKey: cert{Content: bootstrapCerts["server-ca.key"]},
		ClientCA:    cert{Content: bootstrapCerts["client-ca.crt"]},
		ClientCAKey: cert{Content: bootstrapCerts["client-ca.key"]},
	}

	return bootstrap, nil
}

func podExec(ctx context.Context, clientset *kubernetes.Clientset, config *rest.Config, namespace, name string, command []string) ([]byte, error) {
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(name).
		Namespace(namespace).
		SubResource("exec")

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("error adding to scheme: %v", err)
	}

	parameterCodec := runtime.NewParameterCodec(scheme)

	req.VersionedParams(&corev1.PodExecOptions{
		Command: command,
		Stdout:  true,
	}, parameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("error while creating Executor: %v", err)
	}

	var stdout bytes.Buffer

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
	})

	if err != nil {
		return nil, fmt.Errorf("error in Stream: %v", err)
	}

	return stdout.Bytes(), nil
}
