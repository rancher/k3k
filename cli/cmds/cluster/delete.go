package cluster

import (
	"context"
	"errors"

	"github.com/rancher/k3k/cli/cmds"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewDeleteCmd() *cli.Command {
	return &cli.Command{
		Name:            "delete",
		Usage:           "Delete an existing cluster",
		UsageText:       "k3kcli cluster delete [command options] NAME",
		Action:          delete,
		Flags:           cmds.CommonFlags,
		HideHelpCommand: true,
	}
}

func delete(clx *cli.Context) error {
	ctx := context.Background()

	if clx.NArg() != 1 {
		return cli.ShowSubcommandHelp(clx)
	}

	name := clx.Args().First()
	if name == k3kcluster.ClusterInvalidName {
		return errors.New("invalid cluster name")
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

	logrus.Infof("deleting [%s] cluster", name)

	cluster := v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cmds.Namespace(),
		},
	}
	return ctrlClient.Delete(ctx, &cluster)
}
