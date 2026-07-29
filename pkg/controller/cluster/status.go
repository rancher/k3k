package cluster

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/api/meta"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/k3s"
)

const (
	// Condition Types
	ConditionReady = "Ready"

	// Condition Reasons
	ReasonValidationFailed   = "ValidationFailed"
	ReasonProvisioning       = "Provisioning"
	ReasonProvisioned        = "Provisioned"
	ReasonProvisioningFailed = "ProvisioningFailed"
	ReasonTerminating        = "Terminating"
	ReasonRestoring          = "Restoring"
	ReasonRestoringFailed    = "RestoringFailed"
	ReasonRestored           = "Restored"

	ActionReconciling = "Reconciling"
)

func (c *ClusterReconciler) updateStatus(ctx context.Context, cluster *v1beta1.Cluster, reconcileErr error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Updating Cluster Conditions")

	if !cluster.DeletionTimestamp.IsZero() {
		cluster.Status.Phase = v1beta1.ClusterTerminating
		meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:    ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonTerminating,
			Message: "Cluster is being terminated",
		})

		return
	}

	// Handle validation errors specifically to set the Pending phase.
	if errors.Is(reconcileErr, ErrClusterValidation) {
		cluster.Status.Phase = v1beta1.ClusterPending
		meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:    ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonValidationFailed,
			Message: reconcileErr.Error(),
		})

		c.Eventf(cluster, nil, corev1.EventTypeWarning, ReasonValidationFailed, ActionReconciling, reconcileErr.Error())

		return
	}

	if errors.Is(reconcileErr, k3s.ErrServerNotReady) {
		cluster.Status.Phase = v1beta1.ClusterProvisioning
		meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:    ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonProvisioning,
			Message: reconcileErr.Error(),
		})

		return
	}

	reason := ReasonProvisioningFailed
	msg := "Cluster successfully provisioned"
	// if previous state was restoring, then we update the condition reason
	if cluster.Status.Phase == v1beta1.ClusterRestoring {
		reason = ReasonRestoringFailed
		msg = "Cluster successfully restored"
	}

	// If there's an error, but it's not a validation error, the cluster is in a failed state.
	if reconcileErr != nil {
		cluster.Status.Phase = v1beta1.ClusterFailed
		meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:    ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: reconcileErr.Error(),
		})

		c.Eventf(cluster, nil, corev1.EventTypeWarning, reason, ActionReconciling, reconcileErr.Error())

		return
	}

	// If we reach here, everything is successful.
	cluster.Status.Phase = v1beta1.ClusterReady
	newCondition := metav1.Condition{
		Type:    ConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: msg,
	}

	// Only emit event on transition to Ready
	if !meta.IsStatusConditionPresentAndEqual(cluster.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		c.Eventf(cluster, nil, corev1.EventTypeNormal, reason, ActionReconciling, newCondition.Message)
	}

	meta.SetStatusCondition(&cluster.Status.Conditions, newCondition)
}
