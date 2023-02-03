package cluster

import (
	"context"
	"errors"
	"os"

	"github.com/galal-hussein/k3k/cli/cmds"
	"github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	Scheme = runtime.NewScheme()
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
			Value:       "v1.26.1+k3s1",
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
	logrus.Infof("creating a new cluster [%s]", name)
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

	return ctrlClient.Create(ctx, cluster)
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
		},
	}
}
