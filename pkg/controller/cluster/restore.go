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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

const (
	// Restore Condition Types
	RestoringCondition       = "Restoring"
	RestoreFailedCondition   = "Failed"
	RestoreCompleteCondition = "Complete"

	// Restore Condition Reasons
	RestoreReasonInProgress = "RestoreInProgress"
	RestoreReasonFailed     = "RestoreFailed"
	RestoreReasonCompleted  = "RestoreCompleted"
)

var ErrRestoreJobFailed = errors.New("cluster restore job failed")

// restore will trigger a restoration loop for virtual cluster which consists of the following steps:
// 1. Scale down to 0 pods
// 2. Start a restoration job that mounts the cluster's PV and run restoration
// 3. Check restoration job status if completed then exit and trigger normal reconcile
func (c *ClusterReconciler) restore(ctx context.Context, cluster *v1beta1.Cluster, restoreObj *v1beta1.ETCDRestore) error {
	log := ctrl.LoggerFrom(ctx)

	token, err := c.token(ctx, cluster)
	if err != nil {
		return err
	}

	var ETCDSnapshotObj v1beta1.EtcdSnapshot

	if err := c.Client.Get(ctx, types.NamespacedName{Name: restoreObj.Spec.SnapshotName, Namespace: cluster.Namespace}, &ETCDSnapshotObj); err != nil {
		return err
	}

	log.V(1).Info("scale down servers to 0", "snapshotName", ETCDSnapshotObj.Name, "address", ETCDSnapshotObj.Status.Filename)

	cluster.Spec.Servers = new(int32(0))
	s := server.New(cluster, c.Client, token, c.K3SServerImage, c.K3SServerImagePullPolicy, c.ServerImagePullSecrets, &ETCDSnapshotObj)

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
		log.V(1).Info("restore still in progress, scaling down servers")
		return errors.New("restore is still in progress")
	}

	// Phase 2: start a restoration job with the same k3s image
	job, err := s.RestoreJob(ctx, restoreObj.Name, &ETCDSnapshotObj)
	if err != nil {
		return err
	}

	_, err = controllerutil.CreateOrUpdate(ctx, c.Client, job, func() error {
		if err := controllerutil.SetControllerReference(cluster, job, c.Client.Scheme()); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	// wait until the restoration job is completed
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(job), job); err != nil {
		return err
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return ErrRestoreJobFailed
		}

		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			log.V(1).Info("snapshot has been restored successfully", "snapshot", ETCDSnapshotObj.Status.Filename)
			return nil
		}
	}

	log.V(1).Info("restore still in progress, restore job still in progress")

	return errors.New("restore is still in progress")
}

// findEligibleRestore will list restore objects for the passed cluster, if any active restore
// is in progress will be returned otherwise it will pick the oldest pending restore object.
func (c *ClusterReconciler) findEligibleRestore(ctx context.Context, cluster *v1beta1.Cluster) (*v1beta1.ETCDRestore, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Finding eligible restore object")

	var restoreList v1beta1.ETCDRestoreList

	if err := c.Client.List(ctx, &restoreList, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, err
	}

	var oldestObj *v1beta1.ETCDRestore

	for _, restoreObj := range restoreList.Items {
		if restoreObj.Spec.ClusterName != cluster.Name {
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

		if oldestObj == nil || restoreObj.CreationTimestamp.Before(&oldestObj.CreationTimestamp) {
			oldestObj = &restoreObj
			continue
		}
	}

	return oldestObj, nil
}

func setRestoreCondition(status *v1beta1.ETCDRestoreStatus, conditionType string, condStatus metav1.ConditionStatus, reason, msg string) bool {
	return meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:    conditionType,
		Status:  condStatus,
		Reason:  reason,
		Message: msg,
	})
}

func (c *ClusterReconciler) updateRestoreStatus(restore *v1beta1.ETCDRestore, lastErr error) {
	status := &restore.Status

	// handle errors
	if lastErr != nil {
		err := lastErr.Error()

		if errors.Is(lastErr, ErrRestoreJobFailed) {
			setRestoreCondition(status, RestoringCondition, metav1.ConditionFalse, "", err)
			setRestoreCondition(status, RestoreFailedCondition, metav1.ConditionTrue, RestoreReasonFailed, err)
		}

		return
	}

	// no errors means restoration completed
	msg := fmt.Sprintf("Completed restore of %s snapshot", restore.Spec.SnapshotName)
	setRestoreCondition(status, RestoringCondition, metav1.ConditionFalse, "", msg)
	setRestoreCondition(status, RestoreCompleteCondition, metav1.ConditionTrue, RestoreReasonCompleted, msg)
}

func isRestoreInProgress(restore *v1beta1.ETCDRestore) bool {
	if restore == nil || restore.Status.Conditions == nil {
		return false
	}

	return meta.IsStatusConditionPresentAndEqual(restore.Status.Conditions, RestoringCondition, metav1.ConditionTrue)
}

func isRestoreComplete(restore *v1beta1.ETCDRestore) bool {
	if restore == nil || restore.Status.Conditions == nil {
		return false
	}

	return meta.IsStatusConditionPresentAndEqual(restore.Status.Conditions, RestoreCompleteCondition, metav1.ConditionTrue)
}

func isRestoreFailed(restore *v1beta1.ETCDRestore) bool {
	if restore == nil || restore.Status.Conditions == nil {
		return false
	}

	return meta.IsStatusConditionPresentAndEqual(restore.Status.Conditions, RestoreFailedCondition, metav1.ConditionTrue)
}
