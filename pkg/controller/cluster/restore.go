package cluster

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
	// RestoreReasonPersistenceValidationFailed is set when validating a cluster persistence mode fails.
	RestoreReasonPersistenceValidationFailed = "PersistenceValidationFailed"
)

var (
	// ErrRestoreSnapshotValidation This error indicates validation for snapshot is invalid
	ErrRestoreSnapshotValidation = errors.New("snapshot validation error")
	// ErrRestoreClusterPersistenceValidation This error indicates validation for cluster persistence mode is invalid
	ErrRestoreClusterPersistenceValidation = errors.New("restoration requires dynamic persistence mode")
	// ErrRestoreJobFailure This error indicates restoration job has failed
	ErrRestoreJobFailure = errors.New("restoration job has failed")
)

// restore will trigger a restoration for virtual cluster which consists of the following phases:
// 1. Scale down servers to 0 pods.
// 2. Start a restoration jobs that mounts the each server PVC and run restoration for init server and scrub data for other servers.
// 3. Check restoration jobs status if completed then exit and trigger normal reconcile.
//
// Only snapshot validation, cluster persistence validation, and job failure will be considered as final error and will mark
// the restore as failed, otherwise the reconciler will attempt the restoration again.
//
// The returned bool is true while scaling down the servers or the restoration jobs are still in progress, in that case
// the returned error only describes the current progress and is not a failure.
func (c *Reconciler) restore(ctx context.Context, cluster *v1beta1.Cluster, restoreObj *v1beta1.EtcdRestore) (bool, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("EtcdRestore", restoreObj.Name)

	// fail if the cluster is using ephemeral storage
	if cluster.Spec.Persistence.Type != v1beta1.DynamicPersistenceMode {
		return false, ErrRestoreClusterPersistenceValidation
	}

	token, err := c.token(ctx, cluster)
	if err != nil {
		return false, err
	}

	var snapshot v1beta1.EtcdSnapshot

	if err := c.Client.Get(ctx, types.NamespacedName{Name: restoreObj.Spec.SnapshotRef.Name, Namespace: cluster.Namespace}, &snapshot); err != nil {
		if apierrors.IsNotFound(err) {
			return false, fmt.Errorf("%w: %w", ErrRestoreSnapshotValidation, err)
		}

		return false, err
	}

	if err := c.validateSnapshot(cluster, snapshot); err != nil {
		return false, fmt.Errorf("%w: %w", ErrRestoreSnapshotValidation, err)
	}

	origServerCount := int(*cluster.Spec.Servers)
	cluster.Spec.Servers = new(int32(0))
	s := server.New(cluster, c.Client, token, c.K3SServerImage, c.K3SServerImagePullPolicy, c.ServerImagePullSecrets)

	if err := c.server(ctx, cluster, s, nil); err != nil {
		return false, err
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
		return false, err
	}

	if len(serverPods.Items) > 0 {
		return true, errors.New("scaling down servers is in progress")
	}

	// Phase 2: start a restoration jobs with the same k3s image
	jobList, err := s.RestoreJobs(ctx, restoreObj, &snapshot, origServerCount)
	if err != nil {
		return false, err
	}

	completeCount := 0

	for _, job := range jobList.Items {
		_, err = controllerutil.CreateOrUpdate(ctx, c.Client, &job, func() error {
			return controllerutil.SetControllerReference(restoreObj, &job, c.Client.Scheme())
		})
		if err != nil {
			return false, err
		}

		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return false, fmt.Errorf("%w: %s", ErrRestoreJobFailure, cond.Message)
			}

			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				completeCount++
			}
		}
	}

	if completeCount < origServerCount {
		return true, errors.New("restoration job is in progress")
	}

	log.Info("Restore jobs completed successfully")

	return false, nil
}

// findEligibleRestore will list restore objects for the passed cluster, if any active restore
// is being deleted or in progress then it will be returned otherwise it will pick the oldest pending restore object.
func (c *Reconciler) findEligibleRestore(ctx context.Context, cluster *v1beta1.Cluster) (*v1beta1.EtcdRestore, error) {
	var restoreList v1beta1.EtcdRestoreList

	if err := c.Client.List(ctx, &restoreList, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, err
	}

	var oldestRestoreObj *v1beta1.EtcdRestore

	for _, restoreObj := range restoreList.Items {
		if restoreObj.Spec.ClusterRef == nil || restoreObj.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		// return restore objects being deleted regardless of their state so the finalizer can be removed
		if !restoreObj.DeletionTimestamp.IsZero() {
			return &restoreObj, nil
		}

		// return the restore object currently in progress
		if isRestoreUnknown(&restoreObj) {
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

		if errors.Is(lastErr, ErrRestoreSnapshotValidation) {
			setRestoreCondition(status, RestoreSucceededCondition, metav1.ConditionFalse, RestoreReasonValidationFailed, err)
			return
		}

		if errors.Is(lastErr, ErrRestoreClusterPersistenceValidation) {
			setRestoreCondition(status, RestoreSucceededCondition, metav1.ConditionFalse, RestoreReasonPersistenceValidationFailed, err)
			return
		}

		if errors.Is(lastErr, ErrRestoreJobFailure) {
			setRestoreCondition(status, RestoreSucceededCondition, metav1.ConditionFalse, RestoreReasonFailed, err)
			return
		}

		// any other error will be treated as transient error
		setRestoreCondition(status, RestoreSucceededCondition, metav1.ConditionUnknown, RestoreReasonInProgress, err)

		return
	}

	// no errors means restoration completed
	setRestoreCondition(status, RestoreSucceededCondition, metav1.ConditionTrue, RestoreReasonCompleted, fmt.Sprintf("Completed restore of %s snapshot", restore.Spec.SnapshotRef.Name))
}

func isRestoreUnknown(restore *v1beta1.EtcdRestore) bool {
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
	if snapshot.Spec.ClusterRef == nil {
		return errors.New("cluster ref is missing")
	}

	// validation for snapshot
	if snapshot.Spec.ClusterRef.Name != cluster.Name {
		return errors.New("cluster name does not match the snapshot cluster reference")
	}

	if snapshot.Status.Filename == "" {
		return errors.New("snapshot filename is missing")
	}

	if snapshot.Status.Filename == "" {
		return errors.New("snapshot filename is missing")
	}

	name := snapshot.Status.Filename

	if filepath.Base(name) != name || !filepath.IsLocal(name) {
		return fmt.Errorf("invalid snapshot filename %q: must be a plain file name", name)
	}

	snapshotFilenameRegex := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	if !snapshotFilenameRegex.MatchString(name) {
		return fmt.Errorf("invalid snapshot filename %q: contains invalid characters", name)
	}

	return nil
}

func (c *Reconciler) finalizeRestore(ctx context.Context, restoreObj *v1beta1.EtcdRestore) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("EtcdRestore", restoreObj.Name)

	log.V(1).Info("Deleting EtcdRestore")

	// check if restoreJobs still in progress
	var restoreJobs batchv1.JobList

	listOpts := []client.ListOption{
		client.InNamespace(restoreObj.Namespace),
	}
	if err := c.Client.List(ctx, &restoreJobs, listOpts...); err != nil {
		return reconcile.Result{}, err
	}

	for i := range restoreJobs.Items {
		job := &restoreJobs.Items[i]
		if !metav1.IsControlledBy(job, restoreObj) {
			continue
		}

		if isJobInProgress(job) {
			return reconcile.Result{}, fmt.Errorf("%s: restore job is in progress", job.Name)
		}
	}

	// Remove finalizer from the restore and update it only when all restore jobs are finished
	if controllerutil.RemoveFinalizer(restoreObj, etcdRestoreFinalizerName) {
		log.Info("Deleting Restore removing finalizer")

		if err := c.Client.Update(ctx, restoreObj); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func isJobInProgress(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Status == corev1.ConditionTrue && (cond.Type == batchv1.JobComplete || cond.Type == batchv1.JobFailed) {
			return false
		}
	}

	return true
}
