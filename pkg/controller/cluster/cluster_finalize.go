package cluster

import (
	"context"
	"fmt"
	"reflect"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (c *ClusterReconciler) finalizeCluster(ctx context.Context, cluster v1alpha1.Cluster) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("finalizeCluster")

	// remove finalizer from the server pods and update them.
	matchingLabels := ctrlruntimeclient.MatchingLabels(map[string]string{"role": "server"})
	listOpts := &ctrlruntimeclient.ListOptions{Namespace: cluster.Namespace}
	matchingLabels.ApplyToList(listOpts)

	var podList v1.PodList
	if err := c.Client.List(ctx, &podList, listOpts); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	for _, pod := range podList.Items {
		if controllerutil.ContainsFinalizer(&pod, etcdPodFinalizerName) {
			controllerutil.RemoveFinalizer(&pod, etcdPodFinalizerName)
			if err := c.Client.Update(ctx, &pod); err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	if err := c.unbindNodeProxyClusterRole(ctx, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if controllerutil.ContainsFinalizer(&cluster, clusterFinalizerName) {
		// remove finalizer from the cluster and update it.
		controllerutil.RemoveFinalizer(&cluster, clusterFinalizerName)
		if err := c.Client.Update(ctx, &cluster); err != nil {
			return reconcile.Result{}, err
		}
	}
	return reconcile.Result{}, nil
}

func (c *ClusterReconciler) unbindNodeProxyClusterRole(ctx context.Context, cluster *v1alpha1.Cluster) error {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
	if err := c.Client.Get(ctx, types.NamespacedName{Name: "k3k-node-proxy"}, clusterRoleBinding); err != nil {
		return fmt.Errorf("failed to get or find k3k-node-proxy ClusterRoleBinding: %w", err)
	}

	subjectName := controller.SafeConcatNameWithPrefix(cluster.Name, agent.SharedNodeAgentName)

	var cleanedSubjects []rbacv1.Subject
	for _, subject := range clusterRoleBinding.Subjects {
		if subject.Name != subjectName || subject.Namespace != cluster.Namespace {
			cleanedSubjects = append(cleanedSubjects, subject)
		}
	}

	// if no subject was removed, all good
	if reflect.DeepEqual(clusterRoleBinding.Subjects, cleanedSubjects) {
		return nil
	}

	clusterRoleBinding.Subjects = cleanedSubjects
	return c.Client.Update(ctx, clusterRoleBinding)
}
