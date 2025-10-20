package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
)

func (c *ClusterReconciler) finalizeCluster(ctx context.Context, cluster *v1beta1.Cluster) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Deleting Cluster")

	// Set the Terminating phase and condition
	cluster.Status.Phase = v1beta1.ClusterTerminating
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:    ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  ReasonTerminating,
		Message: "Cluster is being terminated",
	})

	if err := c.unbindClusterRoles(ctx, cluster); err != nil {
		return reconcile.Result{}, err
	}

	// Deallocate ports for kubelet and webhook if used
	if cluster.Spec.Mode == v1beta1.SharedClusterMode && cluster.Spec.MirrorHostNodes {
		log.V(1).Info("dellocating ports for kubelet and webhook")

		if err := c.PortAllocator.DeallocateKubeletPort(ctx, cluster.Name, cluster.Namespace, cluster.Status.KubeletPort); err != nil {
			return reconcile.Result{}, err
		}

		if err := c.PortAllocator.DeallocateWebhookPort(ctx, cluster.Name, cluster.Namespace, cluster.Status.WebhookPort); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Remove finalizer from the cluster and update it only when all resources are cleaned up
	if controllerutil.RemoveFinalizer(cluster, clusterFinalizerName) {
		log.Info("Deleting Cluster removing finalizer")

		if err := c.Client.Update(ctx, cluster); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (c *ClusterReconciler) unbindClusterRoles(ctx context.Context, cluster *v1beta1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Unbinding ClusterRoles")

	clusterRoles := []string{"k3k-kubelet-node", "k3k-priorityclass"}

	var err error

	for _, clusterRole := range clusterRoles {
		var clusterRoleBinding rbacv1.ClusterRoleBinding
		if getErr := c.Client.Get(ctx, types.NamespacedName{Name: clusterRole}, &clusterRoleBinding); getErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to get or find %s ClusterRoleBinding: %w", clusterRole, getErr))
			continue
		}

		clusterSubject := rbacv1.Subject{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      controller.SafeConcatNameWithPrefix(cluster.Name, agent.SharedNodeAgentName),
			Namespace: cluster.Namespace,
		}

		// remove the clusterSubject from the ClusterRoleBinding
		cleanedSubjects := slices.DeleteFunc(clusterRoleBinding.Subjects, func(subject rbacv1.Subject) bool {
			return reflect.DeepEqual(subject, clusterSubject)
		})

		if !reflect.DeepEqual(clusterRoleBinding.Subjects, cleanedSubjects) {
			clusterRoleBinding.Subjects = cleanedSubjects

			if updateErr := c.Client.Update(ctx, &clusterRoleBinding); updateErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to update %s ClusterRoleBinding: %w", clusterRole, updateErr))
			}
		}
	}

	return err
}
