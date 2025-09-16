package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
)

const (
	pvcControllerName = "pvc-syncer-controller"
	pvcFinalizerName  = "pvc.k3k.io/finalizer"
)

type PVCReconciler struct {
	*SyncerContext
}

// AddPVCSyncer adds persistentvolumeclaims syncer controller to k3k-kubelet
func AddPVCSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	reconciler := PVCReconciler{
		SyncerContext: &SyncerContext{
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
			VirtualClient:    virtMgr.GetClient(),
			HostClient:       hostMgr.GetClient(),
			Translator: translate.ToHostTranslator{
				ClusterName:      clusterName,
				ClusterNamespace: clusterNamespace,
			},
		},
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, pvcControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&v1.PersistentVolumeClaim{}).
		WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *PVCReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1alpha1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	// check for pvc config
	syncConfig := cluster.Spec.Sync.PersistentVolumeClaims

	// If syncing is disabled, only process deletions to allow for cleanup.
	if !syncConfig.Enabled {
		return object.GetDeletionTimestamp() != nil
	}

	labelSelector := labels.SelectorFromSet(syncConfig.Selector)
	if labelSelector.Empty() {
		return true
	}

	return labelSelector.Matches(labels.Set(object.GetLabels()))
}

func (r *PVCReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	var (
		virtPVC v1.PersistentVolumeClaim
		cluster v1alpha1.Cluster
	)

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.VirtualClient.Get(ctx, req.NamespacedName, &virtPVC); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedPVC := r.pvc(&virtPVC)
	if err := controllerutil.SetControllerReference(&cluster, syncedPVC, r.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtPVC.DeletionTimestamp.IsZero() {
		// deleting the synced pvc if exists
		if err := r.HostClient.Delete(ctx, syncedPVC); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		// remove the finalizer after cleaning up the synced pvc
		if controllerutil.RemoveFinalizer(&virtPVC, pvcFinalizerName) {
			if err := r.VirtualClient.Update(ctx, &virtPVC); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtPVC, pvcFinalizerName) {
		if err := r.VirtualClient.Update(ctx, &virtPVC); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create the pvc on host
	log.Info("creating the persistent volume claim for the first time on the host cluster")

	// note that we dont need to update the PVC on the host cluster, only syncing the PVC to allow being
	// handled by the host cluster.
	return reconcile.Result{}, ctrlruntimeclient.IgnoreAlreadyExists(r.HostClient.Create(ctx, syncedPVC))
}

func (r *PVCReconciler) pvc(obj *v1.PersistentVolumeClaim) *v1.PersistentVolumeClaim {
	hostPVC := obj.DeepCopy()
	r.Translator.TranslateTo(hostPVC)

	return hostPVC
}
