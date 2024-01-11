package cluster

import (
	"context"

	"github.com/rancher/k3k/cli/cmds"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	clusterDeleteFlags = []cli.Flag{
		cli.StringFlag{
			Name:        "name",
			Usage:       "name of the cluster",
			Destination: &name,
		},
	}
)

func delete(clx *cli.Context) error {
	ctx := context.Background()

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

	logrus.Infof("deleting [%s] cluster", name)
	cluster := v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	return ctrlClient.Delete(ctx, &cluster)
}
