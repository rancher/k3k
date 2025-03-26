package cmds

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CreateConfig struct {
	token                string
	clusterCIDR          string
	serviceCIDR          string
	servers              int
	agents               int
	serverArgs           cli.StringSlice
	agentArgs            cli.StringSlice
	persistenceType      string
	storageClassName     string
	version              string
	mode                 string
	kubeconfigServerHost string
}

func NewClusterCreateCmd() *cli.Command {
	createConfig := &CreateConfig{}
	createFlags := NewCreateFlags(createConfig)

	return &cli.Command{
		Name:            "create",
		Usage:           "Create new cluster",
		UsageText:       "k3kcli cluster create [command options] NAME",
		Action:          createAction(createConfig),
		Flags:           append(CommonFlags, createFlags...),
		HideHelpCommand: true,
	}
}

func createAction(config *CreateConfig) cli.ActionFunc {
	return func(clx *cli.Context) error {
		ctx := context.Background()

		if clx.NArg() != 1 {
			return cli.ShowSubcommandHelp(clx)
		}

		name := clx.Args().First()
		if name == k3kcluster.ClusterInvalidName {
			return errors.New("invalid cluster name")
		}

		restConfig, err := loadRESTConfig()
		if err != nil {
			return err
		}

		ctrlClient, err := client.New(restConfig, client.Options{
			Scheme: Scheme,
		})
		if err != nil {
			return err
		}

		namespace := Namespace(name)

		ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := ctrlClient.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}

			logrus.Infof(`Creating namespace [%s]`, namespace)

			if err := ctrlClient.Create(ctx, ns); err != nil {
				return err
			}
		}

		if strings.Contains(config.version, "+") {
			orig := config.version
			config.version = strings.Replace(config.version, "+", "-", -1)
			logrus.Warnf("Invalid K3s docker reference version: '%s'. Using '%s' instead", orig, config.version)
		}

		if config.token != "" {
			logrus.Info("Creating cluster token secret")

			obj := k3kcluster.TokenSecretObj(config.token, name, namespace)

			if err := ctrlClient.Create(ctx, &obj); err != nil {
				return err
			}
		}

		logrus.Infof("Creating cluster [%s] in namespace [%s]", name, namespace)

		cluster := newCluster(name, namespace, config)

		cluster.Spec.Expose = &v1alpha1.ExposeConfig{
			NodePort: &v1alpha1.NodePortConfig{},
		}

		// add Host IP address as an extra TLS-SAN to expose the k3k cluster
		url, err := url.Parse(restConfig.Host)
		if err != nil {
			return err
		}

		host := strings.Split(url.Host, ":")
		if config.kubeconfigServerHost != "" {
			host = []string{config.kubeconfigServerHost}
		}

		cluster.Spec.TLSSANs = []string{host[0]}

		if err := ctrlClient.Create(ctx, cluster); err != nil {
			if apierrors.IsAlreadyExists(err) {
				logrus.Infof("Cluster [%s] already exists", name)
			} else {
				return err
			}
		}

		logrus.Infof("Extracting Kubeconfig for [%s] cluster", name)

		logrus.Infof("waiting for cluster to be available..")

		// retry every 5s for at most 2m, or 25 times
		availableBackoff := wait.Backoff{
			Duration: 5 * time.Second,
			Cap:      2 * time.Minute,
			Steps:    25,
		}

		cfg := kubeconfig.New()

		var kubeconfig *clientcmdapi.Config

		if err := retry.OnError(availableBackoff, apierrors.IsNotFound, func() error {
			kubeconfig, err = cfg.Extract(ctx, ctrlClient, cluster, host[0])
			return err
		}); err != nil {
			return err
		}

		return writeKubeconfigFile(cluster, kubeconfig)
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
			ServerArgs:  config.serverArgs.Value(),
			AgentArgs:   config.agentArgs.Value(),
			Version:     config.version,
			Mode:        v1alpha1.ClusterMode(config.mode),
			Persistence: v1alpha1.PersistenceConfig{
				Type:             v1alpha1.PersistenceMode(config.persistenceType),
				StorageClassName: ptr.To(config.storageClassName),
			},
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

	return cluster
}
