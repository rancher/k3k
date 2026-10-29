package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
)

const (
	ingressControllerName = "ingress-syncer-controller"
	ingressFinalizerName  = "ingress.k3k.io/finalizer"
)

// IngressReconciler syncs the Ingresses of the virtual cluster to the host cluster.
type IngressReconciler struct {
	*Context
}

// AddIngressSyncer adds ingress syncer controller to the manager of the virtual cluster
func AddIngressSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	reconciler := IngressReconciler{
		Context: &Context{
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

	name := reconciler.Translator.TranslateName(clusterNamespace, ingressControllerName)

	// watch the synced Ingresses on the host cluster, so that status changes made by
	// the host ingress controller are propagated back to the virtual cluster
	hostIngressSource := source.Kind(
		hostMgr.GetCache(),
		&networkingv1.Ingress{},
		handler.TypedEnqueueRequestsFromMapFunc(reconciler.virtualIngressRequest),
		predicate.NewTypedPredicateFuncs(reconciler.filterHostIngress),
	)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&networkingv1.Ingress{}, builder.WithPredicates(predicate.NewPredicateFuncs(reconciler.filterResources))).
		WatchesRawSource(hostIngressSource).
		Complete(&reconciler)
}

// filterHostIngress only accepts host Ingresses that were synced from this virtual cluster
// and that still match the sync configuration.
func (r *IngressReconciler) filterHostIngress(object *networkingv1.Ingress) bool {
	if object.GetLabels()[translate.ClusterNameLabel] != r.ClusterName {
		return false
	}

	return r.filterResources(object)
}

// virtualIngressRequest maps a host Ingress to the virtual Ingress it was synced from.
func (*IngressReconciler) virtualIngressRequest(_ context.Context, object *networkingv1.Ingress) []reconcile.Request {
	annotations := object.GetAnnotations()

	name := annotations[translate.ResourceNameAnnotation]
	namespace := annotations[translate.ResourceNamespaceAnnotation]

	if name == "" || namespace == "" {
		return nil
	}

	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}}
}

func (r *IngressReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1beta1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	// check for ingressConfig
	syncConfig := cluster.Spec.Sync.Ingresses

	// If syncing is disabled, only process deletions to allow for cleanup.
	if !syncConfig.Enabled {
		return object.GetDeletionTimestamp() != nil
	}

	labelSelector, err := controller.SyncSelector(syncConfig.Selector, syncConfig.LabelSelector) //nolint:staticcheck
	if err != nil {
		return false
	}

	if labelSelector.Empty() {
		return true
	}

	return labelSelector.Matches(labels.Set(object.GetLabels()))
}

// Reconcile creates, updates or deletes the host Ingress matching a virtual one.
func (r *IngressReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	log.Info("reconciling ingress object")

	var (
		virtIngress networkingv1.Ingress
		cluster     v1beta1.Cluster
	)

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	appliedSync := cluster.Spec.Sync.DeepCopy()

	// If a policy is applied to the virtual cluster we need to use its SyncConfig, if available
	if cluster.Status.Policy != nil && cluster.Status.Policy.Sync != nil {
		appliedSync = cluster.Status.Policy.Sync
	}

	syncConfig := appliedSync.Ingresses

	if err := r.VirtualClient.Get(ctx, req.NamespacedName, &virtIngress); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedIngress := r.ingress(&virtIngress, syncConfig.DisableTLSSecretTranslation)

	if err := controllerutil.SetOwnerReference(&cluster, syncedIngress, r.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtIngress.DeletionTimestamp.IsZero() {
		// deleting the synced ingress if exists
		if err := r.HostClient.Delete(ctx, syncedIngress); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced ingress
		if controllerutil.RemoveFinalizer(&virtIngress, ingressFinalizerName) {
			if err := r.VirtualClient.Update(ctx, &virtIngress); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist

	if controllerutil.AddFinalizer(&virtIngress, ingressFinalizerName) {
		if err := r.VirtualClient.Update(ctx, &virtIngress); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create or update the ingress on host
	var hostIngress networkingv1.Ingress
	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: syncedIngress.Name, Namespace: r.ClusterNamespace}, &hostIngress); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the ingress for the first time on the host cluster")
			return reconcile.Result{}, r.HostClient.Create(ctx, syncedIngress)
		}

		return reconcile.Result{}, err
	}

	ctrl.LoggerFrom(ctx).V(1).Info("updating ingress status on the host cluster")

	if err := r.HostClient.Update(ctx, syncedIngress); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, r.syncStatus(ctx, &virtIngress, &hostIngress)
}

// syncStatus copies the load balancer status of the host Ingress to the virtual Ingress.
func (r *IngressReconciler) syncStatus(ctx context.Context, virtIngress, hostIngress *networkingv1.Ingress) error {
	if equality.Semantic.DeepEqual(virtIngress.Status.LoadBalancer, hostIngress.Status.LoadBalancer) {
		return nil
	}

	ctrl.LoggerFrom(ctx).V(1).Info("updating ingress status in the virtual cluster")

	orig := virtIngress.DeepCopy()
	virtIngress.Status.LoadBalancer = hostIngress.Status.LoadBalancer

	return r.VirtualClient.Status().Patch(ctx, virtIngress, ctrlruntimeclient.MergeFrom(orig))
}

func (r *IngressReconciler) ingress(obj *networkingv1.Ingress, disableTLSSecretTranslation bool) *networkingv1.Ingress {
	hostIngress := obj.DeepCopy()
	r.Translator.TranslateTo(hostIngress)

	for _, rule := range hostIngress.Spec.Rules {
		// modify services in rules to point to the synced services
		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service != nil {
					path.Backend.Service.Name = r.Translator.TranslateName(obj.GetNamespace(), path.Backend.Service.Name)
				}
			}
		}
	}

	// TLS Secret translation disable, return early without translating TLS secrets in the ingress spec
	if disableTLSSecretTranslation {
		return hostIngress
	}
	// ensure tls secrets are also translated
	for i := range hostIngress.Spec.TLS {
		if hostIngress.Spec.TLS[i].SecretName != "" {
			hostIngress.Spec.TLS[i].SecretName = r.Translator.TranslateName(obj.GetNamespace(), hostIngress.Spec.TLS[i].SecretName)
		}
	}

	return hostIngress
}
