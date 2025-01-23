package cluster

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rancher/k3k/cli/cmds"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var Scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)
}

var (
	name                 string
	token                string
	clusterCIDR          string
	serviceCIDR          string
	servers              int64
	agents               int64
	serverArgs           cli.StringSlice
	agentArgs            cli.StringSlice
	persistenceType      string
	storageClassName     string
	version              string
	mode                 string
	kubeconfigServerHost string

	clusterCreateFlags = []cli.Flag{
		&cli.StringFlag{
			Name:        "name",
			Usage:       "name of the cluster",
			Destination: &name,
		},
		&cli.Int64Flag{
			Name:        "servers",
			Usage:       "number of servers",
			Destination: &servers,
			Value:       1,
		},
		&cli.Int64Flag{
			Name:        "agents",
			Usage:       "number of agents",
			Destination: &agents,
		},
		&cli.StringFlag{
			Name:        "token",
			Usage:       "token of the cluster",
			Destination: &token,
		},
		&cli.StringFlag{
			Name:        "cluster-cidr",
			Usage:       "cluster CIDR",
			Destination: &clusterCIDR,
		},
		&cli.StringFlag{
			Name:        "service-cidr",
			Usage:       "service CIDR",
			Destination: &serviceCIDR,
		},
		&cli.StringFlag{
			Name:        "persistence-type",
			Usage:       "Persistence mode for the nodes (ephermal, static, dynamic)",
			Value:       server.EphermalNodesType,
			Destination: &persistenceType,
		},
		&cli.StringFlag{
			Name:        "storage-class-name",
			Usage:       "Storage class name for dynamic persistence type",
			Destination: &storageClassName,
		},
		&cli.StringSliceFlag{
			Name:  "server-args",
			Usage: "servers extra arguments",
			Value: &serverArgs,
		},
		&cli.StringSliceFlag{
			Name:  "agent-args",
			Usage: "agents extra arguments",
			Value: &agentArgs,
		},
		&cli.StringFlag{
			Name:        "version",
			Usage:       "k3s version",
			Destination: &version,
			Value:       "v1.26.1-k3s1",
		},
		&cli.StringFlag{
			Name:        "mode",
			Usage:       "k3k mode type",
			Destination: &mode,
			Value:       "shared",
		},
		&cli.StringFlag{
			Name:        "kubeconfig-server",
			Usage:       "override the kubeconfig server host",
			Destination: &kubeconfigServerHost,
			Value:       "",
		},
	}
)

func create(clx *cli.Context) error {
	ctx := context.Background()
	if err := validateCreateFlags(); err != nil {
		return err
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", cmds.Kubeconfig)
	if err != nil {
		return err
	}

	ctrlClient, err := client.New(restConfig, client.Options{
		Scheme: Scheme,
	})

	if err != nil {
		return err
	}
	if token != "" {
		logrus.Infof("Creating cluster token secret")
		obj := k3kcluster.TokenSecretObj(token, name, cmds.Namespace())
		if err := ctrlClient.Create(ctx, &obj); err != nil {
			return err
		}
	}
	logrus.Infof("Creating a new cluster [%s]", name)
	cluster := newCluster(
		name,
		cmds.Namespace(),
		mode,
		token,
		int32(servers),
		int32(agents),
		clusterCIDR,
		serviceCIDR,
		serverArgs.Value(),
		agentArgs.Value(),
	)

	cluster.Spec.Expose = &v1alpha1.ExposeConfig{
		NodePort: &v1alpha1.NodePortConfig{
			Enabled: true,
		},
	}

	// add Host IP address as an extra TLS-SAN to expose the k3k cluster
	url, err := url.Parse(restConfig.Host)
	if err != nil {
		return err
	}
	host := strings.Split(url.Host, ":")
	if kubeconfigServerHost != "" {
		host = []string{kubeconfigServerHost}
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

	pwd, err := os.Getwd()
	if err != nil {
		return err
	}

	logrus.Infof(`You can start using the cluster with: 

	export KUBECONFIG=%s
	kubectl cluster-info
	`, filepath.Join(pwd, cluster.Name+"-kubeconfig.yaml"))

	kubeconfigData, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return err
	}

	return os.WriteFile(cluster.Name+"-kubeconfig.yaml", kubeconfigData, 0644)
}

func validateCreateFlags() error {
	if persistenceType != server.EphermalNodesType &&
		persistenceType != server.DynamicNodesType {
		return errors.New("invalid persistence type")
	}
	if name == "" {
		return errors.New("empty cluster name")
	}
	if name == k3kcluster.ClusterInvalidName {
		return errors.New("invalid cluster name")
	}
	if servers <= 0 {
		return errors.New("invalid number of servers")
	}
	if cmds.Kubeconfig == "" && os.Getenv("KUBECONFIG") == "" {
		return errors.New("empty kubeconfig")
	}
	if mode != "shared" && mode != "virtual" {
		return errors.New(`mode should be one of "shared" or "virtual"`)
	}

	return nil
}

func newCluster(name, namespace, mode, token string, servers, agents int32, clusterCIDR, serviceCIDR string, serverArgs, agentArgs []string) *v1alpha1.Cluster {
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
			Servers:     &servers,
			Agents:      &agents,
			ClusterCIDR: clusterCIDR,
			ServiceCIDR: serviceCIDR,
			ServerArgs:  serverArgs,
			AgentArgs:   agentArgs,
			Version:     version,
			Mode:        v1alpha1.ClusterMode(mode),
			Persistence: &v1alpha1.PersistenceConfig{
				Type:             persistenceType,
				StorageClassName: storageClassName,
			},
		},
	}
	if token != "" {
		cluster.Spec.TokenSecretRef = &v1.SecretReference{
			Name:      k3kcluster.TokenSecretName(name),
			Namespace: namespace,
		}
	}
	return cluster
}
