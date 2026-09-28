package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

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

// ServiceReconciler syncs the Services of the virtual cluster to the host cluster.
type ServiceReconciler struct {
	*Context
}

// AddServiceSyncer adds service syncer controller to the manager of the virtual cluster
func AddServiceSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}

	reconciler := ServiceReconciler{
		Context: &Context{
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
		For(&corev1.Service{}, builder.WithPredicates(
			predicate.NewPredicateFuncs(reconciler.filterResources),
			// the status patch below changes only the status of the virtual
			// Service: that must not start another reconcile
			ignoreStatusOnlyUpdates(),
		)).
		// The host sets the LoadBalancer status; watch the host copies for it
		// instead of polling.
		WatchesRawSource(source.Kind(hostMgr.GetCache(), &corev1.Service{},
			handler.TypedEnqueueRequestsFromMapFunc(reconciler.virtualServiceFor),
			loadBalancerStatusChanged())).
		Complete(&reconciler)
}

// virtualServiceFor maps a host Service copy of this virtual cluster back to
// its virtual Service.
func (r *ServiceReconciler) virtualServiceFor(_ context.Context, obj *corev1.Service) []reconcile.Request {
	if obj.GetLabels()[translate.ClusterNameLabel] != r.ClusterName {
		return nil
	}

	name := obj.GetAnnotations()[translate.ResourceNameAnnotation]
	if name == "" {
		return nil
	}

	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Name:      name,
		Namespace: obj.GetAnnotations()[translate.ResourceNamespaceAnnotation],
	}}}
}

// loadBalancerStatusChanged passes host Service events whose LoadBalancer
// status changed. Other host changes (for example the update the syncer
// itself makes) are ignored.
func loadBalancerStatusChanged() predicate.TypedPredicate[*corev1.Service] {
	return predicate.TypedFuncs[*corev1.Service]{
		CreateFunc: func(event.TypedCreateEvent[*corev1.Service]) bool { return false },
		DeleteFunc: func(event.TypedDeleteEvent[*corev1.Service]) bool { return false },
		UpdateFunc: func(e event.TypedUpdateEvent[*corev1.Service]) bool {
			return !equality.Semantic.DeepEqual(e.ObjectOld.Status.LoadBalancer, e.ObjectNew.Status.LoadBalancer)
		},
		GenericFunc: func(event.TypedGenericEvent[*corev1.Service]) bool { return false },
	}
}

// ignoreStatusOnlyUpdates drops update events of virtual Services where only
// the status changed. Services have no metadata.generation, so
// GenerationChangedPredicate cannot be used.
func ignoreStatusOnlyUpdates() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldSvc, okOld := e.ObjectOld.(*corev1.Service)
			newSvc, okNew := e.ObjectNew.(*corev1.Service)

			if !okOld || !okNew {
				return true
			}

			return !equality.Semantic.DeepEqual(oldSvc.Spec, newSvc.Spec) ||
				!equality.Semantic.DeepEqual(oldSvc.Labels, newSvc.Labels) ||
				!equality.Semantic.DeepEqual(oldSvc.Annotations, newSvc.Annotations) ||
				!equality.Semantic.DeepEqual(oldSvc.Finalizers, newSvc.Finalizers) ||
				!oldSvc.DeletionTimestamp.Equal(newSvc.DeletionTimestamp)
		},
	}
}

// Reconcile creates, updates or deletes the host Service matching a virtual one. The
// cluster's own kubernetes and kube-dns Services are left alone.
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

	// host events reach the reconciler without the virtual-side filter
	if !r.filterResources(&virtService) {
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
			return reconcile.Result{}, r.HostClient.Create(ctx, syncedService)
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

	return reconcile.Result{}, r.syncStatus(ctx, &virtService, &hostService)
}

// syncStatus copies the host service's LoadBalancer status back to the virtual
// service so in-cluster consumers (e.g. external-dns) see the assigned ingress.
// Host status changes requeue the virtual service through the host watch.
func (r *ServiceReconciler) syncStatus(ctx context.Context, virtService, hostService *corev1.Service) error {
	if virtService.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return nil
	}

	if equality.Semantic.DeepEqual(virtService.Status.LoadBalancer, hostService.Status.LoadBalancer) {
		return nil
	}

	orig := virtService.DeepCopy()
	virtService.Status.LoadBalancer = hostService.Status.LoadBalancer

	return r.VirtualClient.Status().Patch(ctx, virtService, ctrlruntimeclient.MergeFrom(orig))
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

func (r *ServiceReconciler) service(obj *corev1.Service) *corev1.Service {
	hostService := obj.DeepCopy()
	r.Translator.TranslateTo(hostService)
	// don't sync finalizers to the host
	return hostService
}
