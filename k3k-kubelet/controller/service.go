package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
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
	serviceSyncerController = "service-syncer-controller"
	serviceFinalizerName    = "service.k3k.io/finalizer"
)

type ServiceReconciler struct {
	clusterName      string
	clusterNamespace string

	virtualClient ctrlruntimeclient.Client
	hostClient    ctrlruntimeclient.Client
	Scheme        *runtime.Scheme
	HostScheme    *runtime.Scheme
	Translator    translate.ToHostTranslator
}

// AddServiceSyncer adds service syncer controller to the manager of the virtual cluster
func AddServiceSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, serviceSyncConfig v1alpha1.ServiceSyncConfig) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}

	reconciler := ServiceReconciler{
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,

		virtualClient: virtMgr.GetClient(),
		hostClient:    hostMgr.GetClient(),
		Scheme:        virtMgr.GetScheme(),
		HostScheme:    hostMgr.GetScheme(),
		Translator:    translator,
	}

	labelSelector := labels.SelectorFromSet(serviceSyncConfig.Selector)
	if labelSelector.Empty() {
		labelSelector = labels.Everything()
	}

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(serviceSyncerController).
		For(&v1.Service{}).WithEventFilter(predicate.NewPredicateFuncs(func(object ctrlruntimeclient.Object) bool {
		return labelSelector.Matches(labels.Set(object.GetLabels()))
	})).
		Complete(&reconciler)
}

func (r *ServiceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.clusterName, "clusterNamespace", r.clusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	if req.Name == "kubernetes" || req.Name == "kube-dns" {
		return reconcile.Result{}, nil
	}

	var (
		virtService v1.Service
		cluster     v1alpha1.Cluster
	)

	if err := r.hostClient.Get(ctx, types.NamespacedName{Name: r.clusterName, Namespace: r.clusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.virtualClient.Get(ctx, req.NamespacedName, &virtService); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedService := r.service(&virtService)
	if err := controllerutil.SetControllerReference(&cluster, syncedService, r.HostScheme); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtService.DeletionTimestamp.IsZero() {
		// deleting the synced service if exists
		if err := r.hostClient.Delete(ctx, syncedService); err != nil {
			return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
		}

		// remove the finalizer after cleaning up the synced service
		if controllerutil.ContainsFinalizer(&virtService, serviceFinalizerName) {
			controllerutil.RemoveFinalizer(&virtService, serviceFinalizerName)

			if err := r.virtualClient.Update(ctx, &virtService); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if !controllerutil.ContainsFinalizer(&virtService, serviceFinalizerName) {
		controllerutil.AddFinalizer(&virtService, serviceFinalizerName)

		if err := r.virtualClient.Update(ctx, &virtService); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create or update the service on host
	var hostService v1.Service
	if err := r.hostClient.Get(ctx, types.NamespacedName{Name: syncedService.Name, Namespace: r.clusterNamespace}, &hostService); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the service for the first time on the host cluster")
			return reconcile.Result{}, r.hostClient.Create(ctx, syncedService)
		}

		return reconcile.Result{}, err
	}

	log.Info("updating service on the host cluster")

	return reconcile.Result{}, r.hostClient.Update(ctx, syncedService)
}

func (s *ServiceReconciler) service(obj *v1.Service) *v1.Service {
	hostService := obj.DeepCopy()
	s.Translator.TranslateTo(hostService)
	// don't sync finalizers to the host
	return hostService
}
