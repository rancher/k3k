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
	serviceSyncerController = "service-syncer-controller"
	maxConcurrentReconciles = 1
	serviceFinalizerName    = "service.k3k.io/finalizer"
)

type ServiceReconciler struct {
	virtualClient    ctrlruntimeclient.Client
	hostClient       ctrlruntimeclient.Client
	clusterName      string
	clusterNamespace string
	Scheme           *runtime.Scheme
	HostScheme       *runtime.Scheme
	logger           *log.Logger
	Translator       translate.ToHostTranslator
}

// AddServiceSyncer adds service syncer controller to the manager of the virtual cluster
func AddServiceSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, logger *log.Logger) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}
	// initialize a new Reconciler
	reconciler := ServiceReconciler{
		virtualClient:    virtMgr.GetClient(),
		hostClient:       hostMgr.GetClient(),
		Scheme:           virtMgr.GetScheme(),
		HostScheme:       hostMgr.GetScheme(),
		logger:           logger.Named(serviceSyncerController),
		Translator:       translator,
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,
	}

	return ctrl.NewControllerManagedBy(virtMgr).
		For(&v1.Service{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(&reconciler)
}

func (s *ServiceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := s.logger.With("Cluster", s.clusterName, "Service", req.NamespacedName)

	if req.Name == "kubernetes" || req.Name == "kube-dns" {
		return reconcile.Result{}, nil
	}

	var (
		virtService v1.Service
		hostService v1.Service
		cluster     v1alpha1.Cluster
	)
	// getting the cluster for setting the controller reference
	if err := s.hostClient.Get(ctx, types.NamespacedName{Name: s.clusterName, Namespace: s.clusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := s.virtualClient.Get(ctx, req.NamespacedName, &virtService); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedService := s.service(&virtService)
	if err := controllerutil.SetControllerReference(&cluster, syncedService, s.HostScheme); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtService.DeletionTimestamp.IsZero() {
		// deleting the synced service if exists
		if err := s.hostClient.Delete(ctx, syncedService); err != nil {
			return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
		}

		// remove the finalizer after cleaning up the synced service
		if controllerutil.ContainsFinalizer(&virtService, serviceFinalizerName) {
			controllerutil.RemoveFinalizer(&virtService, serviceFinalizerName)

			if err := s.virtualClient.Update(ctx, &virtService); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if !controllerutil.ContainsFinalizer(&virtService, serviceFinalizerName) {
		controllerutil.AddFinalizer(&virtService, serviceFinalizerName)

		if err := s.virtualClient.Update(ctx, &virtService); err != nil {
			return reconcile.Result{}, err
		}
	}
	// create or update the service on host
	if err := s.hostClient.Get(ctx, types.NamespacedName{Name: syncedService.Name, Namespace: s.clusterNamespace}, &hostService); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the service for the first time on the host cluster")
			return reconcile.Result{}, s.hostClient.Create(ctx, syncedService)
		}

		return reconcile.Result{}, err
	}

	log.Info("updating service on the host cluster")

	return reconcile.Result{}, s.hostClient.Update(ctx, syncedService)
}

func (s *ServiceReconciler) service(obj *v1.Service) *v1.Service {
	hostService := obj.DeepCopy()
	s.Translator.TranslateTo(hostService)
	// don't sync finalizers to the host
	return hostService
}
