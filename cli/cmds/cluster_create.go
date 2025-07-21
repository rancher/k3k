package cmds

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
)

type CreateConfig struct {
	token                string
	clusterCIDR          string
	serviceCIDR          string
	servers              int
	agents               int
	serverArgs           []string
	agentArgs            []string
	serverEnvs           []string
	agentEnvs            []string
	persistenceType      string
	storageClassName     string
	storageRequestSize   string
	version              string
	mode                 string
	kubeconfigServerHost string
	policy               string
	mirrorHostNodes      bool
	customCertsPath      string
}

func NewClusterCreateCmd(appCtx *AppContext) *cobra.Command {
	createConfig := &CreateConfig{}

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create new cluster",
		Example: "k3kcli cluster create [command options] NAME",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return validateCreateConfig(createConfig)
		},
		RunE: createAction(appCtx, createConfig),
		Args: cobra.ExactArgs(1),
	}

	CobraFlagNamespace(appCtx, cmd.Flags())
	createFlags(cmd, createConfig)

	return cmd
}

func createAction(appCtx *AppContext, config *CreateConfig) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client
		name := args[0]

		if name == k3kcluster.ClusterInvalidName {
			return errors.New("invalid cluster name")
		}

		if config.mode == string(v1alpha1.SharedClusterMode) && config.agents != 0 {
			return errors.New("invalid flag, --agents flag is only allowed in virtual mode")
		}

		namespace := appCtx.Namespace(name)

		if err := createNamespace(ctx, client, namespace, config.policy); err != nil {
			return err
		}

		if strings.Contains(config.version, "+") {
			orig := config.version
			config.version = strings.ReplaceAll(config.version, "+", "-")
			logrus.Warnf("Invalid K3s docker reference version: '%s'. Using '%s' instead", orig, config.version)
		}

		if config.token != "" {
			logrus.Info("Creating cluster token secret")

			obj := k3kcluster.TokenSecretObj(config.token, name, namespace)

			if err := client.Create(ctx, &obj); err != nil {
				return err
			}
		}

		if config.customCertsPath != "" {
			if err := CreateCustomCertsSecrets(ctx, name, namespace, config.customCertsPath, client); err != nil {
				return err
			}
		}

		logrus.Infof("Creating cluster [%s] in namespace [%s]", name, namespace)

		cluster := newCluster(name, namespace, config)

		cluster.Spec.Expose = &v1alpha1.ExposeConfig{
			NodePort: &v1alpha1.NodePortConfig{},
		}

		// add Host IP address as an extra TLS-SAN to expose the k3k cluster
		url, err := url.Parse(appCtx.RestConfig.Host)
		if err != nil {
			return err
		}

		host := strings.Split(url.Host, ":")
		if config.kubeconfigServerHost != "" {
			host = []string{config.kubeconfigServerHost}
		}

		cluster.Spec.TLSSANs = []string{host[0]}

		if err := client.Create(ctx, cluster); err != nil {
			if apierrors.IsAlreadyExists(err) {
				logrus.Infof("Cluster [%s] already exists", name)
			} else {
				return err
			}
		}

		logrus.Infof("Waiting for cluster to be available..")

		if err := waitForCluster(ctx, client, cluster); err != nil {
			return fmt.Errorf("failed to wait for cluster to become ready (status: %s): %w", cluster.Status.Phase, err)
		}

		logrus.Infof("Extracting Kubeconfig for [%s] cluster", name)

		// retry every 5s for at most 2m, or 25 times
		availableBackoff := wait.Backoff{
			Duration: 5 * time.Second,
			Cap:      2 * time.Minute,
			Steps:    25,
		}

		cfg := kubeconfig.New()

		var kubeconfig *clientcmdapi.Config

		if err := retry.OnError(availableBackoff, apierrors.IsNotFound, func() error {
			kubeconfig, err = cfg.Generate(ctx, client, cluster, host[0])
			return err
		}); err != nil {
			return err
		}

		return writeKubeconfigFile(cluster, kubeconfig, "")
	}
}

func newCluster(name, namespace string, config *CreateConfig) *v1alpha1.Cluster {
	cluster := &v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: "k3k.io/v1alpha1",
		},
		Spec: v1alpha1.ClusterSpec{
			Servers:     ptr.To(int32(config.servers)),
			Agents:      ptr.To(int32(config.agents)),
			ClusterCIDR: config.clusterCIDR,
			ServiceCIDR: config.serviceCIDR,
			ServerArgs:  config.serverArgs,
			AgentArgs:   config.agentArgs,
			ServerEnvs:  env(config.serverEnvs),
			AgentEnvs:   env(config.agentEnvs),
			Version:     config.version,
			Mode:        v1alpha1.ClusterMode(config.mode),
			Persistence: v1alpha1.PersistenceConfig{
				Type:               v1alpha1.PersistenceMode(config.persistenceType),
				StorageClassName:   ptr.To(config.storageClassName),
				StorageRequestSize: config.storageRequestSize,
			},
			MirrorHostNodes: config.mirrorHostNodes,
		},
	}
	if config.storageClassName == "" {
		cluster.Spec.Persistence.StorageClassName = nil
	}

	if config.token != "" {
		cluster.Spec.TokenSecretRef = &v1.SecretReference{
			Name:      k3kcluster.TokenSecretName(name),
			Namespace: namespace,
		}
	}

	if config.customCertsPath != "" {
		cluster.Spec.CustomCAs = v1alpha1.CustomCAs{
			Enabled: true,
			Sources: v1alpha1.CredentialSources{
				ClientCA: v1alpha1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "client-ca"),
				},
				ServerCA: v1alpha1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "server-ca"),
				},
				ETCDServerCA: v1alpha1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "etcd-server-ca"),
				},
				ETCDPeerCA: v1alpha1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "etcd-peer-ca"),
				},
				RequestHeaderCA: v1alpha1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "request-header-ca"),
				},
				ServiceAccountToken: v1alpha1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "service-account-token"),
				},
			},
		}
	}

	return cluster
}

func env(envSlice []string) []v1.EnvVar {
	var envVars []v1.EnvVar

	for _, env := range envSlice {
		keyValue := strings.Split(env, "=")
		if len(keyValue) != 2 {
			logrus.Fatalf("incorrect value for environment variable %s", env)
		}

		envVars = append(envVars, v1.EnvVar{
			Name:  keyValue[0],
			Value: keyValue[1],
		})
	}

	return envVars
}

func waitForCluster(ctx context.Context, k8sClient client.Client, cluster *v1alpha1.Cluster) error {
	interval := 5 * time.Second
	timeout := 2 * time.Minute

	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		key := client.ObjectKeyFromObject(cluster)
		if err := k8sClient.Get(ctx, key, cluster); err != nil {
			return false, fmt.Errorf("failed to get resource: %w", err)
		}

		// If resource ready -> stop polling
		if cluster.Status.Phase == v1alpha1.ClusterReady {
			return true, nil
		}

		// If resource failed -> stop polling with an error
		if cluster.Status.Phase == v1alpha1.ClusterFailed {
			return true, fmt.Errorf("cluster creation failed: %s", cluster.Status.Phase)
		}

		// Condition not met, continue polling.
		return false, nil
	})
}

func CreateCustomCertsSecrets(ctx context.Context, name, namespace, customCertsPath string, k8sclient client.Client) error {
	customCAsMap := map[string]string{
		"etcd-peer-ca":          "/etcd/peer-ca",
		"etcd-server-ca":        "/etcd/server-ca",
		"server-ca":             "/server-ca",
		"client-ca":             "/client-ca",
		"request-header-ca":     "/request-header-ca",
		"service-account-token": "/service",
	}

	for certName, fileName := range customCAsMap {
		var (
			certFilePath, keyFilePath string
			cert, key                 []byte
			err                       error
		)

		if certName != "service-account-token" {
			certFilePath = customCertsPath + fileName + ".crt"

			cert, err = os.ReadFile(certFilePath)
			if err != nil {
				return err
			}
		}

		keyFilePath = customCertsPath + fileName + ".key"

		key, err = os.ReadFile(keyFilePath)
		if err != nil {
			return err
		}

		certSecret := caCertSecret(certName, name, namespace, cert, key)

		if err := k8sclient.Create(ctx, certSecret); err != nil {
			return client.IgnoreAlreadyExists(err)
		}
	}

	return nil
}

func caCertSecret(certName, clusterName, clusterNamespace string, cert, key []byte) *v1.Secret {
	return &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      controller.SafeConcatNameWithPrefix(clusterName, certName),
			Namespace: clusterNamespace,
		},
		Type: v1.SecretTypeTLS,
		Data: map[string][]byte{
			v1.TLSCertKey:       cert,
			v1.TLSPrivateKeyKey: key,
		},
	}
}
