package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/galal-hussein/k3k/cli/cmds"
	"github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/galal-hussein/k3k/pkg/controller/util"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	Scheme  = runtime.NewScheme()
	backoff = wait.Backoff{
		Steps:    5,
		Duration: 3 * time.Second,
		Factor:   2,
		Jitter:   0.1,
	}
)

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)
}

var (
	name        string
	token       string
	clusterCIDR string
	serviceCIDR string
	servers     int64
	agents      int64
	serverArgs  cli.StringSlice
	agentArgs   cli.StringSlice
	version     string

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
	cluster.Spec.TLSSANs = []string{
		host[0],
	}

	if err := ctrlClient.Create(ctx, cluster); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logrus.Infof("Cluster [%s] already exists", name)
		} else {
			return err
		}
	}

	logrus.Infof("Extracting Kubeconfig for [%s] cluster", name)
	var kubeconfig []byte
	err = retry.OnError(backoff, apierrors.IsNotFound, func() error {
		kubeconfig, err = extractKubeconfig(ctx, ctrlClient, cluster, host[0])
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
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
	if token == "" {
		return errors.New("empty cluster token")
	}
	if name == "" {
		return errors.New("empty cluster name")
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
		},
	}
}

func extractKubeconfig(ctx context.Context, client client.Client, cluster *v1alpha1.Cluster, serverIP string) ([]byte, error) {
	nn := types.NamespacedName{
		Name:      cluster.Name + "-kubeconfig",
		Namespace: util.ClusterNamespace(cluster),
	}
	var kubeSecret v1.Secret
	if err := client.Get(ctx, nn, &kubeSecret); err != nil {
		return nil, err
	}

	kubeconfig := kubeSecret.Data["kubeconfig.yaml"]
	if kubeconfig == nil {
		return nil, errors.New("empty kubeconfig")
	}

	nn = types.NamespacedName{
		Name:      "k3k-server-service",
		Namespace: util.ClusterNamespace(cluster),
	}
	var k3kService v1.Service
	if err := client.Get(ctx, nn, &k3kService); err != nil {
		return nil, err
	}
	if k3kService.Spec.Type == v1.ServiceTypeNodePort {
		nodePort := k3kService.Spec.Ports[0].NodePort

		restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
		if err != nil {
			return nil, err
		}
		hostURL := fmt.Sprintf("https://%s:%d", serverIP, nodePort)
		restConfig.Host = hostURL

		clientConfig := generateKubeconfigFromRest(restConfig)

		b, err := clientcmd.Write(clientConfig)
		if err != nil {
			return nil, err
		}
		kubeconfig = b
	}
	return kubeconfig, nil
}

func generateKubeconfigFromRest(config *rest.Config) clientcmdapi.Config {
	clusters := make(map[string]*clientcmdapi.Cluster)
	clusters["default-cluster"] = &clientcmdapi.Cluster{
		Server:                   config.Host,
		CertificateAuthorityData: config.CAData,
	}

	contexts := make(map[string]*clientcmdapi.Context)
	contexts["default-context"] = &clientcmdapi.Context{
		Cluster:   "default-cluster",
		Namespace: "default",
		AuthInfo:  "default",
	}

	authinfos := make(map[string]*clientcmdapi.AuthInfo)
	authinfos["default"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: config.CertData,
		ClientKeyData:         config.KeyData,
	}

	clientConfig := clientcmdapi.Config{
		Kind:           "Config",
		APIVersion:     "v1",
		Clusters:       clusters,
		Contexts:       contexts,
		CurrentContext: "default-context",
		AuthInfos:      authinfos,
	}
	return clientConfig
}
