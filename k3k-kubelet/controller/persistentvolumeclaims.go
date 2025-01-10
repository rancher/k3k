package controller

import (
	"context"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/log"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	pvcController    = "pvc-syncer-controller"
	pvcFinalizerName = "pv.k3k.io/finalizer"
)

type PVCReconciler struct {
	virtualClient    ctrlruntimeclient.Client
	hostClient       ctrlruntimeclient.Client
	clusterName      string
	clusterNamespace string
	Scheme           *runtime.Scheme
	HostScheme       *runtime.Scheme
	logger           *log.Logger
	Translater       translate.ToHostTranslater
	//objs             sets.Set[types.NamespacedName]
}

// AddPVCSyncer adds persistentvolumeclaims syncer controller to k3k-kubelet
func AddPVCSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, logger *log.Logger) error {
	translater := translate.ToHostTranslater{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}
	// initialize a new Reconciler
	reconciler := PVCReconciler{
		virtualClient:    virtMgr.GetClient(),
		hostClient:       hostMgr.GetClient(),
		Scheme:           virtMgr.GetScheme(),
		HostScheme:       hostMgr.GetScheme(),
		logger:           logger.Named(pvcController),
		Translater:       translater,
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,
	}
	return ctrl.NewControllerManagedBy(virtMgr).
		For(&v1.PersistentVolumeClaim{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(&reconciler)
}

func (v *PVCReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := v.logger.With("Cluster", v.clusterName, "PersistentVolumeClaim", req.NamespacedName)
	var (
		virtPVC v1.PersistentVolumeClaim
		hostPVC v1.PersistentVolumeClaim
		cluster v1alpha1.Cluster
	)
	if err := v.hostClient.Get(ctx, types.NamespacedName{Name: v.clusterName, Namespace: v.clusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	// handling persistent volume sync
	if err := v.virtualClient.Get(ctx, req.NamespacedName, &virtPVC); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	syncedPVC := v.pvc(&virtPVC)
	if err := controllerutil.SetControllerReference(&cluster, syncedPVC, v.HostScheme); err != nil {
		return reconcile.Result{}, err
	}
	// handle deletion
	if !virtPVC.DeletionTimestamp.IsZero() {
		// deleting the synced service if exists
		if err := v.hostClient.Delete(ctx, syncedPVC); err != nil {
			return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
		}
		// remove the finalizer after cleaning up the synced service
		if controllerutil.ContainsFinalizer(&virtPVC, pvcFinalizerName) {
			controllerutil.RemoveFinalizer(&virtPVC, pvcFinalizerName)
			if err := v.virtualClient.Update(ctx, &virtPVC); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	// getting the cluster for setting the controller reference

	// Add finalizer if it does not exist
	if !controllerutil.ContainsFinalizer(&virtPVC, pvcFinalizerName) {
		controllerutil.AddFinalizer(&virtPVC, pvcFinalizerName)
		if err := v.virtualClient.Update(ctx, &virtPVC); err != nil {
			return reconcile.Result{}, err
		}
	}
	// create or update the pv on host
	if err := v.hostClient.Get(ctx, types.NamespacedName{Name: syncedPVC.Name, Namespace: v.clusterNamespace}, &hostPVC); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the persistent volume for the first time on the host cluster")
			return reconcile.Result{}, v.hostClient.Create(ctx, syncedPVC)
		}
		return reconcile.Result{}, err
	}
	log.Info("updating pvc on the host cluster")
	return reconcile.Result{}, v.hostClient.Update(ctx, syncedPVC)

}

func (v *PVCReconciler) pvc(obj *v1.PersistentVolumeClaim) *v1.PersistentVolumeClaim {
	hostPVC := obj.DeepCopy()
	v.Translater.TranslateTo(hostPVC)
	// don't sync finalizers to the host
	return hostPVC
}
