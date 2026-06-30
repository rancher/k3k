package cmds

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
)

var keepData bool

func NewClusterDeleteCmd(appCtx *AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete",
		Short:   "Delete an existing cluster.",
		Example: "k3kcli cluster delete [command options] NAME",
		RunE:    delete(appCtx),
		Args:    cobra.ExactArgs(1),
	}

	CobraFlagNamespace(appCtx, cmd.Flags())
	cmd.Flags().BoolVar(&keepData, "keep-data", false, "keeps persistence volumes created for the cluster after deletion")

	return cmd
}

func delete(appCtx *AppContext) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client
		name := args[0]

		if name == k3kcluster.ClusterInvalidName {
			return errors.New("invalid cluster name")
		}

		namespace := appCtx.Namespace(name)

		cluster := v1beta1.Cluster{}
		clusterKey := types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}
		if err := client.Get(ctx, clusterKey, &cluster); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("cluster %q not found in namespace %q; use `k3kcli cluster list` to check the namespace, or pass --namespace", name, namespace)
			}

			return err
		}

		logrus.Infof("Deleting '%s' cluster in namespace '%s'", name, namespace)

		// keep bootstrap secrets and tokens if --keep-data flag is passed
		if keepData {
			// skip removing tokenSecret
			if err := RemoveOwnerReferenceFromSecret(ctx, k3kcluster.TokenSecretName(cluster.Name), client, cluster); err != nil {
				return err
			}
		} else {
			matchingLabels := ctrlclient.MatchingLabels(map[string]string{"cluster": cluster.Name, "role": "server"})
			listOpts := ctrlclient.ListOptions{Namespace: cluster.Namespace}
			matchingLabels.ApplyToList(&listOpts)
			deleteOpts := &ctrlclient.DeleteAllOfOptions{ListOptions: listOpts}

			if err := client.DeleteAllOf(ctx, &corev1.PersistentVolumeClaim{}, deleteOpts); err != nil {
				return ctrlclient.IgnoreNotFound(err)
			}
		}

		if err := client.Delete(ctx, &cluster); err != nil {
			return ctrlclient.IgnoreNotFound(err)
		}

		return nil
	}
}

func RemoveOwnerReferenceFromSecret(ctx context.Context, name string, cl ctrlclient.Client, cluster v1beta1.Cluster) error {
	var secret corev1.Secret

	key := types.NamespacedName{
		Name:      name,
		Namespace: cluster.Namespace,
	}

	if err := cl.Get(ctx, key, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			logrus.Warnf("%s secret is not found", name)
			return nil
		}

		return err
	}

	if controllerutil.HasControllerReference(&secret) {
		if err := controllerutil.RemoveOwnerReference(&cluster, &secret, cl.Scheme()); err != nil {
			return err
		}

		return cl.Update(ctx, &secret)
	}

	return nil
}
