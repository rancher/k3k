package cmds

import (
	"context"
	"errors"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var keepData bool

func NewClusterDeleteCmd() *cli.Command {
	return &cli.Command{
		Name:      "delete",
		Usage:     "Delete an existing cluster",
		UsageText: "k3kcli cluster delete [command options] NAME",
		Action:    delete,
		Flags: append(CommonFlags, &cli.BoolFlag{
			Name:        "keep-data",
			Usage:       "keeps persistence volumes created for the cluster after deletion",
			Destination: &keepData,
		}),
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

	logrus.Infof("deleting [%s] cluster", name)

	cluster := v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: Namespace(),
		},
	}
	// keep bootstrap secrets and tokens if --keep-data flag is passed
	if keepData {
		var tokenSecret, webhookSecret v1.Secret
		// skip removing tokenSecret
		key := types.NamespacedName{
			Name:      k3kcluster.TokenSecretName(cluster.Name),
			Namespace: cluster.Namespace,
		}
		if err := ctrlClient.Get(ctx, key, &tokenSecret); err != nil {
			return err
		}
		if err := controllerutil.RemoveOwnerReference(&cluster, &tokenSecret, ctrlClient.Scheme()); err != nil {
			return err
		}
		if err := ctrlClient.Update(ctx, &tokenSecret); err != nil {
			return err
		}

		// skip removing webhook secret
		key = types.NamespacedName{
			Name:      agent.WebhookSecretName(cluster.Name),
			Namespace: cluster.Namespace,
		}
		if err := ctrlClient.Get(ctx, key, &webhookSecret); err != nil {
			return err
		}
		if err := controllerutil.RemoveOwnerReference(&cluster, &webhookSecret, ctrlClient.Scheme()); err != nil {
			return err
		}
		if err := ctrlClient.Update(ctx, &webhookSecret); err != nil {
			return err
		}

	}
	if err := ctrlClient.Delete(ctx, &cluster); err != nil {
		return err
	}

	// make sure to delete pv claims for the cluster if --keep-data is not used
	if !keepData {
		matchingLabels := client.MatchingLabels(map[string]string{"cluster": cluster.Name, "role": "server"})
		listOpts := client.ListOptions{Namespace: cluster.Namespace}
		matchingLabels.ApplyToList(&listOpts)
		deleteOpts := &client.DeleteAllOfOptions{ListOptions: listOpts}

		if err := ctrlClient.DeleteAllOf(ctx, &v1.PersistentVolumeClaim{}, deleteOpts); err != nil {
			return err
		}
	}
	return nil
}
