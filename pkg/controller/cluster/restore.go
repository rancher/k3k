package cluster

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

const (
	// RestoreSucceededCondition is the condition type reporting whether a restoration succeeded.
	RestoreSucceededCondition = "Succeeded"

	// RestoreReasonInProgress is set when a restoration is still in progress.
	RestoreReasonInProgress = "RestoreInProgress"
	// RestoreReasonFailed is set when a restoring a snapshot fails.
	RestoreReasonFailed = "RestoreFailed"
	// RestoreReasonCompleted is set when restoring a snapshot completes.
	RestoreReasonCompleted = "RestoreCompleted"
	// RestoreReasonValidationFailed is set when validating a snapshot fails.
	RestoreReasonValidationFailed = "SnapshotValidationFailed"
)

var (
	// ErrRestoreJobInProgress This error indicates that job for restoring the snapshot is still in progress
	ErrRestoreJobInProgress = errors.New("cluster restore jobs are in progress")
	// ErrRestoreScalingInProgress This error indicates that scaling down servers to 0 is still in progress
	ErrRestoreScalingInProgress = errors.New("scaling down in progress")
	// ErrRestoreSnapshotValidation This error indicates validation for snapshot is invalid
	ErrRestoreSnapshotValidation = errors.New("snapshot validation error")
)

// restore will trigger a restoration for virtual cluster which consists of the following phases:
// 1. Scale down servers to 0 pods
// 2. Start a restoration jobs that mounts the each server PVC and run restoration for init server and scrub data for other servers
// 3. Check restoration jobs status if completed then exit and trigger normal reconcile
func (c *Reconciler) restore(ctx context.Context, cluster *v1beta1.Cluster, restoreObj *v1beta1.EtcdRestore) error {
	log := ctrl.LoggerFrom(ctx)

	// fail if the cluster is using ephemeral storage
	if cluster.Spec.Persistence.Type != v1beta1.DynamicPersistenceMode {
		return errors.New("cluster restoration is only enabled with persistence mode")
	}

	token, err := c.token(ctx, cluster)
	if err != nil {
		return err
	}

	var snapshot v1beta1.EtcdSnapshot

	if err := c.Client.Get(ctx, types.NamespacedName{Name: restoreObj.Spec.SnapshotRef.Name, Namespace: cluster.Namespace}, &snapshot); err != nil {
		return err
	}

	if err := c.validateSnapshot(cluster, snapshot); err != nil {
		return fmt.Errorf("%w: %w", ErrRestoreSnapshotValidation, err)
	}

	origServerCount := int(*cluster.Spec.Servers)
	cluster.Spec.Servers = new(int32(0))
	s := server.New(cluster, c.Client, token, c.K3SServerImage, c.K3SServerImagePullPolicy, c.ServerImagePullSecrets)

	if err := c.server(ctx, cluster, s); err != nil {
		return err
	}

	// make sure that servers scaled down to 0 servers
	matchingLabels := client.MatchingLabels(map[string]string{
		"role":    "server",
		"cluster": cluster.Name,
	})

	listOpts := &client.ListOptions{Namespace: cluster.Namespace}
	matchingLabels.ApplyToList(listOpts)

	var serverPods corev1.PodList
	if err := c.Client.List(ctx, &serverPods, listOpts); err != nil {
		return err
	}

	if len(serverPods.Items) > 0 {
		return ErrRestoreScalingInProgress
	}

	// Phase 2: start a restoration jobs with the same k3s image
	jobList, err := s.RestoreJobs(ctx, restoreObj.Name, &snapshot, origServerCount)
	if err != nil {
		return err
	}

	completeCount := 0

	for _, job := range jobList.Items {
		_, err = controllerutil.CreateOrUpdate(ctx, c.Client, &job, func() error {
			return controllerutil.SetControllerReference(cluster, &job, c.Client.Scheme())
		})
		if err != nil {
			return err
		}

		// wait until the restoration job is completed
		if err := c.Client.Get(ctx, client.ObjectKeyFromObject(&job), &job); err != nil {
			if apierrors.IsNotFound(err) {
				return ErrRestoreJobInProgress
			}

			return err
		}

		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return fmt.Errorf("job has failed to restore the snapshot: %s", cond.Message)
			}

			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				completeCount++
			}
		}
	}

	if completeCount < origServerCount {
		return ErrRestoreJobInProgress
	}

	log.Info("Restore jobs completed successfully")

	return nil
}

// findEligibleRestore will list restore objects for the passed cluster, if any active restore
// is in progress then it will be returned otherwise it will pick the oldest pending restore object.
func (c *Reconciler) findEligibleRestore(ctx context.Context, cluster *v1beta1.Cluster) (*v1beta1.EtcdRestore, error) {
	var restoreList v1beta1.EtcdRestoreList

	if err := c.Client.List(ctx, &restoreList, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, err
	}

	var oldestRestoreObj *v1beta1.EtcdRestore

	for _, restoreObj := range restoreList.Items {
		if restoreObj.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		// return the restore object currently in progress
		if isRestoreInProgress(&restoreObj) {
			return &restoreObj, nil
		}

		// skip failed or completed restores
		if isRestoreComplete(&restoreObj) || isRestoreFailed(&restoreObj) {
			continue
		}

		// FIFO selection for the restore request
		if oldestRestoreObj == nil || restoreObj.CreationTimestamp.Before(&oldestRestoreObj.CreationTimestamp) {
			oldestRestoreObj = &restoreObj
		}
	}

	return oldestRestoreObj, nil
}

func setRestoreCondition(status *v1beta1.EtcdRestoreStatus, conditionType string, condStatus metav1.ConditionStatus, reason, msg string) bool {
	return meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:    conditionType,
		Status:  condStatus,
		Reason:  reason,
		Message: msg,
	})
}

// updateRestoreStatus updates the restore object status, errors are handled by checking for in progress errors
// like scaling down the servers or restoration job still in progress, the rest of the errors will set the restoration
// to a failed status
func (*Reconciler) updateRestoreStatus(restore *v1beta1.EtcdRestore, lastErr error) {
	status := &restore.Status

	// handle errors
	if lastErr != nil {
		err := lastErr.Error()

		if !errors.Is(lastErr, ErrRestoreJobInProgress) && !errors.Is(lastErr, ErrRestoreScalingInProgress) && !apierrors.IsConflict(lastErr) {
			setRestoreCondition(status, RestoreSucceededCondition, metav1.ConditionFalse, RestoreReasonFailed, err)
			return
		}

		if errors.Is(lastErr, ErrRestoreSnapshotValidation) {
			setRestoreCondition(status, RestoreSucceededCondition, metav1.ConditionFalse, RestoreReasonValidationFailed, err)
		}

		setRestoreCondition(status, RestoreSucceededCondition, metav1.ConditionUnknown, RestoreReasonInProgress, "Restore is in progress")

		return
	}

	// no errors means restoration completed
	setRestoreCondition(status, RestoreSucceededCondition, metav1.ConditionTrue, RestoreReasonCompleted, fmt.Sprintf("Completed restore of %s snapshot", restore.Spec.SnapshotRef.Name))
}

func isRestoreInProgress(restore *v1beta1.EtcdRestore) bool {
	if restore == nil || restore.Status.Conditions == nil {
		return false
	}

	return meta.IsStatusConditionPresentAndEqual(restore.Status.Conditions, RestoreSucceededCondition, metav1.ConditionUnknown)
}

func isRestoreComplete(restore *v1beta1.EtcdRestore) bool {
	if restore == nil || restore.Status.Conditions == nil {
		return false
	}

	return meta.IsStatusConditionPresentAndEqual(restore.Status.Conditions, RestoreSucceededCondition, metav1.ConditionTrue)
}

func isRestoreFailed(restore *v1beta1.EtcdRestore) bool {
	if restore == nil || restore.Status.Conditions == nil {
		return false
	}

	return meta.IsStatusConditionPresentAndEqual(restore.Status.Conditions, RestoreSucceededCondition, metav1.ConditionFalse)
}

func (*Reconciler) validateSnapshot(cluster *v1beta1.Cluster, snapshot v1beta1.EtcdSnapshot) error {
	// validation for snapshot
	if snapshot.Spec.ClusterRef.Name != cluster.Name {
		return errors.New("cluster name does not match the snapshot cluster reference")
	}

	if snapshot.Status.Filename == "" {
		return errors.New("snapshot filename is missing")
	}

	return nil
}
