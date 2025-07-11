package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (c *ClusterReconciler) finalizeCluster(ctx context.Context, cluster *v1alpha1.Cluster) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("finalizing Cluster")

	// Set the Terminating phase and condition
	cluster.Status.Phase = v1alpha1.ClusterTerminating
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
	if cluster.Spec.Mode == v1alpha1.SharedClusterMode && cluster.Spec.MirrorHostNodes {
		log.Info("dellocating ports for kubelet and webhook")

		if err := c.PortAllocator.DeallocateKubeletPort(ctx, c.Client, cluster.Name, cluster.Namespace, cluster.Status.KubeletPort); err != nil {
			return reconcile.Result{}, err
		}

		if err := c.PortAllocator.DeallocateWebhookPort(ctx, c.Client, cluster.Name, cluster.Namespace, cluster.Status.WebhookPort); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Remove finalizer from the cluster and update it only when all resources are cleaned up
	if controllerutil.RemoveFinalizer(cluster, clusterFinalizerName) {
		if err := c.Client.Update(ctx, cluster); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (c *ClusterReconciler) unbindClusterRoles(ctx context.Context, cluster *v1alpha1.Cluster) error {
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
