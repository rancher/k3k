package syncer

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
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
		For(&corev1.Service{}).WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *ServiceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	if req.Name == "kubernetes" || req.Name == "kube-dns" {
		return reconcile.Result{}, nil
	}

	var (
		virtService corev1.Service
		cluster     v1beta1.Cluster
	)

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.VirtualClient.Get(ctx, req.NamespacedName, &virtService); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedService := r.service(&virtService)

	if err := controllerutil.SetOwnerReference(&cluster, syncedService, r.HostClient.Scheme()); err != nil {
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
	var hostService corev1.Service
	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: syncedService.Name, Namespace: r.ClusterNamespace}, &hostService); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the service for the first time on the host cluster")
			if err := r.HostClient.Create(ctx, syncedService); err != nil {
				return reconcile.Result{}, err
			}
			// requeue to pick up host-assigned status (e.g. LoadBalancer ingress)
			return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
		}

		return reconcile.Result{}, err
	}

	log.Info("updating service on the host cluster")

	// The host apiserver owns IP-family allocation: the host service may have been
	// expanded to dual-stack while the virtual service is single-stack. Re-submitting
	// the virtual family fields is rejected ("must be 'SingleStack' to release the
	// secondary cluster IP"), so preserve the host-allocated values on update.
	syncedService.Spec.ClusterIP = hostService.Spec.ClusterIP
	syncedService.Spec.ClusterIPs = hostService.Spec.ClusterIPs
	syncedService.Spec.IPFamilies = hostService.Spec.IPFamilies
	syncedService.Spec.IPFamilyPolicy = hostService.Spec.IPFamilyPolicy
	syncedService.Spec.HealthCheckNodePort = hostService.Spec.HealthCheckNodePort

	if err := r.HostClient.Update(ctx, syncedService); err != nil {
		return reconcile.Result{}, err
	}

	return r.syncStatus(ctx, &virtService, &hostService)
}

// syncStatus copies the host service's LoadBalancer status back to the virtual
// service so in-cluster consumers (e.g. external-dns) see the assigned ingress.
// The controller only watches virtual services, so as long as the ingress is
// empty it requeues to poll the host side.
func (r *ServiceReconciler) syncStatus(ctx context.Context, virtService, hostService *corev1.Service) (reconcile.Result, error) {
	if virtService.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return reconcile.Result{}, nil
	}

	if !equality.Semantic.DeepEqual(virtService.Status.LoadBalancer, hostService.Status.LoadBalancer) {
		orig := virtService.DeepCopy()
		virtService.Status.LoadBalancer = hostService.Status.LoadBalancer

		if err := r.VirtualClient.Status().Patch(ctx, virtService, ctrlruntimeclient.MergeFrom(orig)); err != nil {
			return reconcile.Result{}, err
		}
	}

	if len(hostService.Status.LoadBalancer.Ingress) == 0 {
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	return reconcile.Result{}, nil
}

func (r *ServiceReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1beta1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	// check for serviceSyncConfig
	syncConfig := cluster.Spec.Sync.Services

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

func (s *ServiceReconciler) service(obj *corev1.Service) *corev1.Service {
	hostService := obj.DeepCopy()
	s.Translator.TranslateTo(hostService)
	// don't sync finalizers to the host
	return hostService
}
