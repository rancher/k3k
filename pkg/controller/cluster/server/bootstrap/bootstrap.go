package bootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/request"
)

const (
	TLSDir = "/var/lib/rancher/k3s/server/tls/"
)

type Data struct {
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

// Fetch requests bootstrap data from k3s using the token and decodes it,
// to avoid double encoding when stored as secret. In case of external datastore we attempt
// to read the certificate files from the server pod directly since k3s bootstrap API is disabled.
func Fetch(ctx context.Context, cluster *v1beta1.Cluster, ip, token string, restConfig *rest.Config, clusterInit bool) (*Data, error) {
	log := ctrl.LoggerFrom(ctx)
	if !clusterInit {
		// read bootstrap data from the server pod
		log.V(1).Info("Fetching bootstrap data from server pod")
		return fetchFromPod(ctx, restConfig, cluster)
	}

	return fetchFromK3sServer(ip, token)
}

func decode(bootstrap *Data) error {
	// client-ca
	decoded, err := base64.StdEncoding.DecodeString(bootstrap.ClientCA.Content)
	if err != nil {
		return err
	}

	bootstrap.ClientCA.Content = string(decoded)

	// client-ca-key
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ClientCAKey.Content)
	if err != nil {
		return err
	}

	bootstrap.ClientCAKey.Content = string(decoded)

	// server-ca
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ServerCA.Content)
	if err != nil {
		return err
	}

	bootstrap.ServerCA.Content = string(decoded)

	// server-ca-key
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ServerCAKey.Content)
	if err != nil {
		return err
	}

	bootstrap.ServerCAKey.Content = string(decoded)

	// etcd-ca
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ETCDServerCA.Content)
	if err != nil {
		return err
	}

	bootstrap.ETCDServerCA.Content = string(decoded)

	// etcd-ca-key
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ETCDServerCAKey.Content)
	if err != nil {
		return err
	}

	bootstrap.ETCDServerCAKey.Content = string(decoded)

	return nil
}

// SaveToSecret marshals the bootstrap data and stores it in a Secret owned by the cluster,
// creating the Secret if it does not exist or updating it otherwise.
func SaveToSecret(ctx context.Context, c client.Client, scheme *runtime.Scheme, cluster *v1beta1.Cluster, data *Data) error {
	bootstrapData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controller.SafeConcatNameWithPrefix(cluster.Name, "bootstrap"),
			Namespace: cluster.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, c, secret, func() error {
		if err := controllerutil.SetControllerReference(cluster, secret, scheme); err != nil {
			return err
		}

		secret.Data = map[string][]byte{
			"bootstrap": bootstrapData,
		}

		return nil
	})

	return err
}

// LoadFromSecret reads the bootstrap data of a certain cluster and returun back the decoded content
func LoadFromSecret(ctx context.Context, client client.Client, cluster *v1beta1.Cluster) (*Data, error) {
	key := types.NamespacedName{
		Name:      controller.SafeConcatNameWithPrefix(cluster.Name, "bootstrap"),
		Namespace: cluster.Namespace,
	}

	var bootstrapSecret corev1.Secret
	if err := client.Get(ctx, key, &bootstrapSecret); err != nil {
		return nil, err
	}

	bootstrapData := bootstrapSecret.Data["bootstrap"]
	if bootstrapData == nil {
		return nil, errors.New("empty bootstrap")
	}

	var bootstrap Data

	err := json.Unmarshal(bootstrapData, &bootstrap)

	return &bootstrap, err
}

func fetchFromPod(ctx context.Context, restConfig *rest.Config, cluster *v1beta1.Cluster) (*Data, error) {
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	// using the first server in the statefulset to get the bootstrap data
	serverPodName := controller.SafeConcatNameWithPrefix(cluster.Name, "server-0")

	bootstrapCerts := map[string]string{
		"server-ca.crt": "",
		"server-ca.key": "",
		"client-ca.crt": "",
		"client-ca.key": "",
	}
	for certName := range bootstrapCerts {
		command := []string{"cat", TLSDir + certName}

		certData, err := podExec(ctx, clientset, restConfig, cluster.Namespace, serverPodName, command)
		if err != nil {
			return nil, err
		}

		bootstrapCerts[certName] = string(certData)
	}

	bootstrap := &Data{
		ServerCA: cert{
			Content: bootstrapCerts["server-ca.crt"],
		},
		ServerCAKey: cert{
			Content: bootstrapCerts["server-ca.key"],
		},
		ClientCA: cert{
			Content: bootstrapCerts["client-ca.crt"],
		},
		ClientCAKey: cert{
			Content: bootstrapCerts["client-ca.key"],
		},
	}

	return bootstrap, nil
}

func fetchFromK3sServer(serviceIP, token string) (*Data, error) {
	var bootstrap Data

	resp, err := request.RequestK3sServer(serviceIP, "/v1-k3s/server-bootstrap", "server", token, nil)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(resp, &bootstrap); err != nil {
		return nil, err
	}

	// we still need to decode each certs since the bootstrap data endpoint base64 encode each cert
	if err := decode(&bootstrap); err != nil {
		return nil, fmt.Errorf("failed to decode bootstrap secret: %w", err)
	}

	return &bootstrap, err
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
		Stderr:  true,
		TTY:     false,
	}, parameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("error while creating Executor: %v", err)
	}

	var stdout, stderr bytes.Buffer

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})
	if err != nil {
		return nil, fmt.Errorf("error in Stream: %v", err)
	}

	return stdout.Bytes(), nil
}
