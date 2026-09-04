package snapshot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	k3sv1 "github.com/k3s-io/api/k3s.cattle.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/k3s"
)

const (
	snapshotController    = "k3k-snapshot-controller"
	snapshotFinalizerName = "snapshot.k3k.io/finalizer"

	// Condition Types
	ConditionReady = "Ready"

	// FailedCreateSnapshotReason is added in an event or condition when a snapshot is failed to be created.
	FailedCreateSnapshotReason = "FailedCreate"
	// SuccessfulCreateSnapshotReason is added in an event or condition when a snapshot is successfully created.
	SuccessfulCreateSnapshotReason = "SuccessfulCreate"
	// FailedDeleteSnapshotReason is added in an event or condition when a snapshot is failed to be deleted.
	FailedDeleteSnapshotReason = "FailedDelete"
	// SuccessfulDeleteSnapshotReason is added in an event when a snapshot is successfully deleted.
	SuccessfulDeleteSnapshotReason = "SuccessfulDelete"

	snapshotReconcilingAction = "Reconciling"
)

var errClusterNotReady = errors.New("cluster is not ready")

type Reconciler struct {
	client.Client
	events.EventRecorder
}

// Add adds a new snapshot controller to the manager
func Add(ctx context.Context, mgr manager.Manager, maxConcurrentReconciles int) error {
	reconciler := Reconciler{
		Client:        mgr.GetClient(),
		EventRecorder: mgr.GetEventRecorder(snapshotController),
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.EtcdSnapshot{}).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
		Complete(&reconciler)
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Reconciling EtcdSnapshot")

	var snapshot v1beta1.EtcdSnapshot
	if err := r.Get(ctx, req.NamespacedName, &snapshot); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// handle snapshot deletion
	if !snapshot.DeletionTimestamp.IsZero() {
		if err := r.finalizeSnapshot(ctx, &snapshot); err != nil {
			if err := r.updateSnapshotStatus(ctx, &snapshot, metav1.ConditionFalse, FailedDeleteSnapshotReason, err.Error()); err != nil {
				return reconcile.Result{}, err
			}

			r.Eventf(&snapshot, nil, corev1.EventTypeWarning, FailedDeleteSnapshotReason, snapshotReconcilingAction, err.Error())

			return reconcile.Result{}, err
		}

		r.Eventf(&snapshot, nil, corev1.EventTypeNormal, SuccessfulDeleteSnapshotReason, snapshotReconcilingAction, "Snapshot was deleted")

		return reconcile.Result{}, nil
	}

	// avoid recreating snapshot if status is populated
	if snapshot.Status.Filename != "" {
		return reconcile.Result{}, nil
	}

	if err := r.reconcileSnapshot(ctx, &snapshot); err != nil {
		if err := r.updateSnapshotStatus(ctx, &snapshot, metav1.ConditionFalse, FailedCreateSnapshotReason, err.Error()); err != nil {
			return reconcile.Result{}, err
		}

		if errors.Is(err, errClusterNotReady) {
			log.V(1).Info("Cluster not ready, requeueing")
			return reconcile.Result{RequeueAfter: time.Second * 10}, nil
		}

		r.Eventf(&snapshot, nil, corev1.EventTypeWarning, FailedCreateSnapshotReason, snapshotReconcilingAction, err.Error())

		return reconcile.Result{}, err
	}

	// only emit event when the file is actually created and populated to the status
	if snapshot.Status.Filename != "" {
		if err := r.updateSnapshotStatus(ctx, &snapshot, metav1.ConditionTrue, SuccessfulCreateSnapshotReason, "Snapshot was created"); err != nil {
			return reconcile.Result{}, err
		}

		r.Eventf(&snapshot, nil, corev1.EventTypeNormal, SuccessfulCreateSnapshotReason, snapshotReconcilingAction, "Snapshot was created")
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) reconcileSnapshot(ctx context.Context, snapshot *v1beta1.EtcdSnapshot) error {
	var cluster v1beta1.Cluster

	nn := types.NamespacedName{
		Name:      snapshot.Spec.ClusterRef.Name,
		Namespace: snapshot.Namespace,
	}

	if err := r.Get(ctx, nn, &cluster); err != nil {
		return err
	}

	if cluster.Status.Phase != v1beta1.ClusterReady {
		return errClusterNotReady
	}

	if controllerutil.AddFinalizer(snapshot, snapshotFinalizerName) {
		return r.Update(ctx, snapshot)
	}

	token, err := k3kcluster.GetClusterToken(ctx, r.Client, &cluster)
	if err != nil {
		return err
	}

	initServerPodName := controller.SafeConcatNameWithPrefix(cluster.Name, "server-0")
	k3sClient := k3s.New(k3s.ClientConfig{
		Token:    token,
		ServerIP: fmt.Sprintf("%s.%s.%s:6443", initServerPodName, server.HeadlessServiceName(cluster.Name), snapshot.Namespace),
	})

	var s3Config *k3s.EtcdS3

	if snapshot.Spec.S3ConfigSecretRef != nil {
		s3Config, err = r.getS3ConfigFromSecret(ctx, snapshot)
		if err != nil {
			return err
		}
	}

	snapshotResp, err := k3sClient.SaveSnapshot(snapshot, s3Config)
	if err != nil {
		return fmt.Errorf("failed to save snapshot for cluster %s: %w", cluster.Name, err)
	}

	if len(snapshotResp.Created) <= 0 {
		return fmt.Errorf("no snapshot found")
	}

	// handling backpopulation
	return r.backpopulateSnapshotStatus(ctx, snapshotResp.Created[0], snapshot, k3sClient, s3Config)
}

func (r *Reconciler) backpopulateSnapshotStatus(ctx context.Context, snapshotName string, snapshot *v1beta1.EtcdSnapshot, k3sClient *k3s.Client, s3Config *k3s.EtcdS3) error {
	var (
		snapshotFileList *k3sv1.ETCDSnapshotFileList
		snapshotFile     *k3sv1.ETCDSnapshotFile
	)

	// we use list snapshot instead of getting the etcdSnapshotFile due to a bug in k3s
	// for agentless servers, where pruning is run on each snapshot save and it does not
	// exclude the agentless nodes https://github.com/k3s-io/k3s/pull/14345
	snapshotFileList, err := k3sClient.ListSnapshots(s3Config)
	if err != nil {
		return err
	}

	for i := range snapshotFileList.Items {
		file := snapshotFileList.Items[i]
		if file.Spec.SnapshotName == snapshotName {
			if s3Config != nil {
				// if s3Config is populated then we intend to backpopulate s3 snapshot
				// so we need to skip local snapshot even if it matches the name
				if strings.HasPrefix(file.Spec.Location, "file://") {
					continue
				}
			}

			snapshotFile = &file

			break
		}
	}

	if snapshotFile == nil {
		return fmt.Errorf("snapshot file %s not found in virtual cluster", snapshotName)
	}

	snapshot.Status = v1beta1.EtcdSnapshotStatus{
		Filename: snapshotFile.Spec.SnapshotName,
	}

	return r.Client.Status().Update(ctx, snapshot)
}

func (r *Reconciler) finalizeSnapshot(ctx context.Context, snapshot *v1beta1.EtcdSnapshot) error {
	if err := r.deleteSnapshot(ctx, snapshot); err != nil {
		return err
	}

	if controllerutil.RemoveFinalizer(snapshot, snapshotFinalizerName) {
		return r.Update(ctx, snapshot)
	}

	return nil
}

func (r *Reconciler) deleteSnapshot(ctx context.Context, snapshot *v1beta1.EtcdSnapshot) error {
	log := log.FromContext(ctx)

	// no op if there is no snapshot file reference
	if snapshot.Status.Filename == "" {
		return nil
	}

	var cluster v1beta1.Cluster

	clusterKey := types.NamespacedName{
		Name:      snapshot.Spec.ClusterRef.Name,
		Namespace: snapshot.Namespace,
	}

	if err := r.Get(ctx, clusterKey, &cluster); err != nil {
		return client.IgnoreNotFound(err)
	}

	// skip deletion if the cluster is terminating
	if !cluster.DeletionTimestamp.IsZero() {
		return nil
	}

	// remove the snapshot from k3s cluster
	log.Info("Deleting snapshot from cluster")

	token, err := k3kcluster.GetClusterToken(ctx, r.Client, &cluster)
	if err != nil {
		return err
	}

	initServerPodName := controller.SafeConcatNameWithPrefix(cluster.Name, "server-0")
	k3sClient := k3s.New(k3s.ClientConfig{
		Token:    token,
		ServerIP: fmt.Sprintf("%s.%s.%s:6443", initServerPodName, server.HeadlessServiceName(cluster.Name), snapshot.Namespace),
	})

	var s3Config *k3s.EtcdS3

	if snapshot.Spec.S3ConfigSecretRef != nil {
		s3Config, err = r.getS3ConfigFromSecret(ctx, snapshot)
		if err != nil {
			return err
		}
	}

	_, err = k3sClient.DeleteSnapshot(snapshot, s3Config)

	// do not return error if snapshot is not found in the virtual cluster
	if err != nil && !errors.Is(err, k3s.ErrSnapshotNotFound) {
		return err
	}

	return nil
}

func (r *Reconciler) updateSnapshotStatus(ctx context.Context, snapshot *v1beta1.EtcdSnapshot, status metav1.ConditionStatus, reason, msg string) error {
	log := log.FromContext(ctx)

	orig := snapshot.DeepCopy()
	meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{
		Type:    ConditionReady,
		Status:  status,
		Reason:  reason,
		Message: msg,
	})

	if !equality.Semantic.DeepEqual(orig.Status, snapshot.Status) {
		log.Info("Updating Snapshot status")

		return r.Client.Status().Update(ctx, snapshot)
	}

	return nil
}
