package controller

import (
	"context"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/log"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	serviceSyncerController = "service-syncer-controller"
	maxConcurrentReconciles = 1
)

// TODO: change into a generic syncer
type ServiceReconciler struct {
	virtualClient    ctrlruntimeclient.Client
	hostClient       ctrlruntimeclient.Client
	clusterName      string
	clusterNamespace string
	Scheme           *runtime.Scheme
	logger           *log.Logger
	Translater       translate.ToHostTranslater
}

// AddServiceSyncer adds service syncer controller to the manager of the virtual cluster
func AddServiceSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, logger *log.Logger) error {
	translater := translate.ToHostTranslater{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}
	// initialize a new Reconciler
	reconciler := ServiceReconciler{
		virtualClient:    virtMgr.GetClient(),
		hostClient:       hostMgr.GetClient(),
		Scheme:           virtMgr.GetScheme(),
		logger:           logger.Named(serviceSyncerController),
		Translater:       translater,
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
	// skip kubernetes service
	if req.Name == "kubernetes" || req.Name == "kube-dns" {
		return reconcile.Result{}, nil
	}
	var (
		virtService v1.Service
		hostService v1.Service
	)
	if err := s.virtualClient.Get(ctx, req.NamespacedName, &virtService); err != nil {
		return reconcile.Result{}, err
	}
	syncedService := s.service(&virtService)
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
	s.Translater.TranslateTo(hostService)
	return hostService
}
