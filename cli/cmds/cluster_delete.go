package cmds

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
)

var (
	keepData  bool
	deleteAll bool
)

func NewClusterDeleteCmd(appCtx *AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete",
		Short:   "Delete an existing cluster.",
		Example: "k3kcli cluster delete [command options] NAME",
		RunE:    delete(appCtx),
		Args:    cobra.MaximumNArgs(1),
	}

	cmd.Flags().BoolVar(&keepData, "keep-data", false, "keeps persistence volumes created for the cluster after deletion")
	cmd.Flags().BoolVarP(&deleteAll, "all", "A", false, "delete all the clusters in the namespace")

	cmd.ValidArgsFunction = completeClusterNames

	CobraFlagNamespace(appCtx, cmd, completeClusterNamespaces)

	return cmd
}

func delete(appCtx *AppContext) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client

		if deleteAll {
			if len(args) > 0 {
				return errors.New("cannot specify a cluster name together with --all")
			}

			if appCtx.namespace == "" {
				return errors.New("--all requires a namespace, set it with --namespace/-n")
			}

			var clusters v1beta1.ClusterList
			if err := client.List(ctx, &clusters, ctrlclient.InNamespace(appCtx.namespace)); err != nil {
				return err
			}

			for i := range clusters.Items {
				if err := deleteCluster(ctx, client, &clusters.Items[i]); err != nil {
					return err
				}
			}

			return nil
		}

		if len(args) != 1 {
			return errors.New("expected exactly one cluster name")
		}

		namespace, name, err := resolveClusterArg(appCtx, args[0])
		if err != nil {
			return err
		}

		if name == k3kcluster.ClusterInvalidName {
			return errors.New("invalid cluster name")
		}

		cluster := v1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		}
		if err := client.Get(ctx, ctrlclient.ObjectKeyFromObject(&cluster), &cluster); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("cluster %q not found in namespace %q", name, namespace)
			}

			return err
		}

		return deleteCluster(ctx, client, &cluster)
	}
}

// resolveClusterArg splits an arg that may be in "namespace/name" form. A bare "name"
// falls back to the k3k-<name> convention (respecting the -n flag) via appCtx.Namespace.
// When both the -n flag and an explicit namespace prefix are given and they disagree an
// error is returned.
func resolveClusterArg(appCtx *AppContext, arg string) (namespace, name string, err error) {
	if ns, clusterName, ok := strings.Cut(arg, "/"); ok {
		if appCtx.namespace != "" && appCtx.namespace != ns {
			return "", "", fmt.Errorf("namespace mismatch: flag --namespace %q conflicts with argument namespace %q", appCtx.namespace, ns)
		}

		return ns, clusterName, nil
	}

	return appCtx.Namespace(arg), arg, nil
}

// deleteCluster removes a cluster and, unless --keep-data is set, its server PersistentVolumeClaims.
func deleteCluster(ctx context.Context, client ctrlclient.Client, cluster *v1beta1.Cluster) error {
	logrus.Infof("Deleting '%s' cluster in namespace '%s'", cluster.Name, cluster.Namespace)

	// keep bootstrap secrets and tokens if --keep-data flag is passed
	if keepData {
		// skip removing tokenSecret
		if err := RemoveOwnerReferenceFromSecret(ctx, k3kcluster.TokenSecretName(cluster.Name), client, *cluster); err != nil {
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

	if err := client.Delete(ctx, cluster); err != nil {
		return ctrlclient.IgnoreNotFound(err)
	}

	return nil
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
