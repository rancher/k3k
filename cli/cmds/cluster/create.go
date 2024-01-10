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
	"github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	"github.com/rancher/k3k/pkg/controller/util"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/authentication/user"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	Scheme  = runtime.NewScheme()
	backoff = wait.Backoff{
		Steps:    5,
		Duration: 20 * time.Second,
		Factor:   2,
		Jitter:   0.1,
	}
)

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)
}

var (
	name             string
	token            string
	clusterCIDR      string
	serviceCIDR      string
	servers          int64
	agents           int64
	serverArgs       cli.StringSlice
	agentArgs        cli.StringSlice
	persistenceType  string
	storageClassName string
	version          string

	clusterCreateFlags = []cli.Flag{
		cli.StringFlag{
			Name:        "name",
			Usage:       "name of the cluster",
			Destination: &name,
		},
		cli.Int64Flag{
			Name:        "servers",
			Usage:       "number of servers",
			Destination: &servers,
			Value:       1,
		},
		cli.Int64Flag{
			Name:        "agents",
			Usage:       "number of agents",
			Destination: &agents,
		},
		cli.StringFlag{
			Name:        "token",
			Usage:       "token of the cluster",
			Destination: &token,
		},
		cli.StringFlag{
			Name:        "cluster-cidr",
			Usage:       "cluster CIDR",
			Destination: &clusterCIDR,
		},
		cli.StringFlag{
			Name:        "service-cidr",
			Usage:       "service CIDR",
			Destination: &serviceCIDR,
		},
		cli.StringFlag{
			Name:        "persistence-type",
			Usage:       "Persistence mode for the nodes (ephermal, static, dynamic)",
			Value:       server.EphermalNodesType,
			Destination: &persistenceType,
		},
		cli.StringFlag{
			Name:        "storage-class-name",
			Usage:       "Storage class name for dynamic persistence type",
			Destination: &storageClassName,
		},
		cli.StringSliceFlag{
			Name:  "server-args",
			Usage: "servers extra arguments",
			Value: &serverArgs,
		},
		cli.StringSliceFlag{
			Name:  "agent-args",
			Usage: "agents extra arguments",
			Value: &agentArgs,
		},
		cli.StringFlag{
			Name:        "version",
			Usage:       "k3s version",
			Destination: &version,
			Value:       "v1.26.1-k3s1",
		},
	}
)

func createCluster(clx *cli.Context) error {
	ctx := context.Background()
	if err := validateCreateFlags(clx); err != nil {
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
	logrus.Infof("Creating a new cluster [%s]", name)
	cluster := newCluster(
		name,
		token,
		int32(servers),
		int32(agents),
		clusterCIDR,
		serviceCIDR,
		serverArgs,
		agentArgs,
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
	cluster.Spec.TLSSANs = []string{host[0]}

	if err := ctrlClient.Create(ctx, cluster); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logrus.Infof("Cluster [%s] already exists", name)
		} else {
			return err
		}
	}

	logrus.Infof("Extracting Kubeconfig for [%s] cluster", name)
	cfg := &kubeconfig.KubeConfig{
		CN:         util.AdminCommonName,
		ORG:        []string{user.SystemPrivilegedGroup},
		ExpiryDate: 0,
	}
	logrus.Infof("waiting for cluster to be available..")
	var kubeconfig []byte
	if err := retry.OnError(backoff, apierrors.IsNotFound, func() error {
		kubeconfig, err = cfg.Extract(ctx, ctrlClient, cluster, host[0])
		if err != nil {
			return err
		}
		return nil
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

	return os.WriteFile(cluster.Name+"-kubeconfig.yaml", kubeconfig, 0644)
}

func validateCreateFlags(clx *cli.Context) error {
	if persistenceType != server.EphermalNodesType &&
		persistenceType != server.DynamicNodesType {
		return errors.New("invalid persistence type")
	}
	if token == "" {
		return errors.New("empty cluster token")
	}
	if name == "" {
		return errors.New("empty cluster name")
	}
	if name == cluster.ClusterInvalidName {
		return errors.New("invalid cluster name")
	}
	if servers <= 0 {
		return errors.New("invalid number of servers")
	}
	if cmds.Kubeconfig == "" && os.Getenv("KUBECONFIG") == "" {
		return errors.New("empty kubeconfig")
	}

	return nil
}

func newCluster(name, token string, servers, agents int32, clusterCIDR, serviceCIDR string, serverArgs, agentArgs []string) *v1alpha1.Cluster {
	return &v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: "k3k.io/v1alpha1",
		},
		Spec: v1alpha1.ClusterSpec{
			Name:        name,
			Token:       token,
			Servers:     &servers,
			Agents:      &agents,
			ClusterCIDR: clusterCIDR,
			ServiceCIDR: serviceCIDR,
			ServerArgs:  serverArgs,
			AgentArgs:   agentArgs,
			Version:     version,
			Persistence: &v1alpha1.PersistenceConfig{
				Type:             persistenceType,
				StorageClassName: storageClassName,
			},
		},
	}
}
