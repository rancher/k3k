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
	serviceControllerName = "service-syncer-controller"
	serviceFinalizerName  = "service.k3k.io/finalizer"
)

type ServiceReconciler struct {
	*SyncerContext
}

// AddServiceSyncer adds service syncer controller to the manager of the virtual cluster
func AddServiceSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}

	reconciler := ServiceReconciler{
		SyncerContext: &SyncerContext{
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
			VirtualClient:    virtMgr.GetClient(),
			HostClient:       hostMgr.GetClient(),
			Translator:       translator,
		},
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, serviceControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&v1.Service{}).WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *ServiceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	if req.Name == "kubernetes" || req.Name == "kube-dns" {
		return reconcile.Result{}, nil
	}

	var (
		virtService v1.Service
		cluster     v1alpha1.Cluster
	)

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.VirtualClient.Get(ctx, req.NamespacedName, &virtService); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedService := r.service(&virtService)
	if err := controllerutil.SetControllerReference(&cluster, syncedService, r.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtService.DeletionTimestamp.IsZero() {
		// deleting the synced service if exists
		if err := r.HostClient.Delete(ctx, syncedService); err != nil {
			return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
		}

		// remove the finalizer after cleaning up the synced service
		if controllerutil.RemoveFinalizer(&virtService, serviceFinalizerName) {
			if err := r.VirtualClient.Update(ctx, &virtService); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtService, serviceFinalizerName) {
		if err := r.VirtualClient.Update(ctx, &virtService); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create or update the service on host
	var hostService v1.Service
	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: syncedService.Name, Namespace: r.ClusterNamespace}, &hostService); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the service for the first time on the host cluster")
			return reconcile.Result{}, r.HostClient.Create(ctx, syncedService)
		}

		return reconcile.Result{}, err
	}

	log.Info("updating service on the host cluster")

	return reconcile.Result{}, r.HostClient.Update(ctx, syncedService)
}

func (r *ServiceReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1alpha1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	// check for serviceSyncConfig
	syncConfig := cluster.Spec.Sync.Services

	if !syncConfig.IsEnabled() {
		return false
	}

	labelSelector := labels.SelectorFromSet(syncConfig.Selector)
	if labelSelector.Empty() {
		labelSelector = labels.Everything()
	}

	return labelSelector.Matches(labels.Set(object.GetLabels()))
}

func (s *ServiceReconciler) service(obj *v1.Service) *v1.Service {
	hostService := obj.DeepCopy()
	s.Translator.TranslateTo(hostService)
	// don't sync finalizers to the host
	return hostService
}
