package cluster

import (
	"errors"

	"k8s.io/apimachinery/pkg/api/meta"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
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
)

func (c *ClusterReconciler) updateStatus(cluster *v1alpha1.Cluster, reconcileErr error) {
	if !cluster.DeletionTimestamp.IsZero() {
		cluster.Status.Phase = v1alpha1.ClusterTerminating
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
		cluster.Status.Phase = v1alpha1.ClusterPending
		meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:    ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonValidationFailed,
			Message: reconcileErr.Error(),
		})

		c.Eventf(cluster, v1.EventTypeWarning, ReasonValidationFailed, reconcileErr.Error())

		return
	}

	if errors.Is(reconcileErr, bootstrap.ErrServerNotReady) {
		cluster.Status.Phase = v1alpha1.ClusterProvisioning
		meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:    ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonProvisioning,
			Message: reconcileErr.Error(),
		})

		return
	}

	// If there's an error, but it's not a validation error, the cluster is in a failed state.
	if reconcileErr != nil {
		cluster.Status.Phase = v1alpha1.ClusterFailed
		meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:    ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonProvisioningFailed,
			Message: reconcileErr.Error(),
		})

		c.Eventf(cluster, v1.EventTypeWarning, ReasonProvisioningFailed, reconcileErr.Error())

		return
	}

	// If we reach here, everything is successful.
	cluster.Status.Phase = v1alpha1.ClusterReady
	newCondition := metav1.Condition{
		Type:    ConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  ReasonProvisioned,
		Message: "Cluster successfully provisioned",
	}

	// Only emit event on transition to Ready
	if !meta.IsStatusConditionPresentAndEqual(cluster.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		c.Eventf(cluster, v1.EventTypeNormal, ReasonProvisioned, newCondition.Message)
	}

	meta.SetStatusCondition(&cluster.Status.Conditions, newCondition)
}
