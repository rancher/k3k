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
	pvcFinalizerName = "pvc.k3k.io/finalizer"
)

type PVCReconciler struct {
	virtualClient    ctrlruntimeclient.Client
	hostClient       ctrlruntimeclient.Client
	clusterName      string
	clusterNamespace string
	Scheme           *runtime.Scheme
	HostScheme       *runtime.Scheme
	logger           *log.Logger
	Translator       translate.ToHostTranslator
}

// AddPVCSyncer adds persistentvolumeclaims syncer controller to k3k-kubelet
func AddPVCSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, logger *log.Logger) error {
	translator := translate.ToHostTranslator{
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
		Translator:       translator,
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

func (r *PVCReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.logger.With("Cluster", r.clusterName, "PersistentVolumeClaim", req.NamespacedName)
	var (
		virtPVC v1.PersistentVolumeClaim
		hostPVC v1.PersistentVolumeClaim
		cluster v1alpha1.Cluster
	)
	if err := r.hostClient.Get(ctx, types.NamespacedName{Name: r.clusterName, Namespace: r.clusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	// handling persistent volume sync
	if err := r.virtualClient.Get(ctx, req.NamespacedName, &virtPVC); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	syncedPVC := r.pvc(&virtPVC)
	if err := controllerutil.SetControllerReference(&cluster, syncedPVC, r.HostScheme); err != nil {
		return reconcile.Result{}, err
	}
	// handle deletion
	if !virtPVC.DeletionTimestamp.IsZero() {
		// deleting the synced service if exists
		if err := r.hostClient.Delete(ctx, syncedPVC); !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		// remove the finalizer after cleaning up the synced service
		if controllerutil.RemoveFinalizer(&virtPVC, pvcFinalizerName) {
			if err := r.virtualClient.Update(ctx, &virtPVC); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	// getting the cluster for setting the controller reference

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtPVC, pvcFinalizerName) {
		if err := r.virtualClient.Update(ctx, &virtPVC); err != nil {
			return reconcile.Result{}, err
		}
	}
	// create or update the pvc on host
	if err := r.hostClient.Get(ctx, types.NamespacedName{Name: syncedPVC.Name, Namespace: r.clusterNamespace}, &hostPVC); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the persistent volume for the first time on the host cluster")
			return reconcile.Result{}, r.hostClient.Create(ctx, syncedPVC)
		}
		return reconcile.Result{}, err
	}
	log.Info("updating pvc on the host cluster")
	return reconcile.Result{}, r.hostClient.Update(ctx, syncedPVC)

}

func (r *PVCReconciler) pvc(obj *v1.PersistentVolumeClaim) *v1.PersistentVolumeClaim {
	hostPVC := obj.DeepCopy()
	r.Translator.TranslateTo(hostPVC)
	return hostPVC
}
