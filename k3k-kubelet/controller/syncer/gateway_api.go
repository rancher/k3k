package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	gatewayAPIControllerName = "gateway_api-syncer-controller"
	gatewayAPIFinalizerName  = "gatewayapi.k3k.io/finalizer"
)

type GatewayAPIReconciler struct {
	*Context
}

func AddGatewayAPISyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	reconciler := GatewayAPIReconciler{
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

	name := reconciler.Translator.TranslateName(clusterNamespace, gatewayAPIControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&gatewayv1.HTTPRoute{}).
		WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *GatewayAPIReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1beta1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	syncConfig := cluster.Spec.Sync.HTTPRoutes

	if !syncConfig.Enabled {
		return object.GetDeletionTimestamp() != nil
	}

	labelSelector := labels.SelectorFromSet(syncConfig.Selector)
	if labelSelector.Empty() {
		return true
	}

	return labelSelector.Matches(labels.Set(object.GetLabels()))
}

func (r *GatewayAPIReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	log.Info("reconciling gateway api httproute object")

	var (
		virtHTTPRoute gatewayv1.HTTPRoute
		cluster       v1beta1.Cluster
	)

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	appliedSync := cluster.Spec.Sync.DeepCopy()
	if cluster.Status.Policy != nil && cluster.Status.Policy.Sync != nil {
		appliedSync = cluster.Status.Policy.Sync
	}

	syncConfig := appliedSync.HTTPRoutes

	if err := r.VirtualClient.Get(ctx, req.NamespacedName, &virtHTTPRoute); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedHTTPRoute := r.httproute(&virtHTTPRoute, syncConfig)

	if err := controllerutil.SetOwnerReference(&cluster, syncedHTTPRoute, r.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	if !virtHTTPRoute.DeletionTimestamp.IsZero() {
		if err := r.HostClient.Delete(ctx, syncedHTTPRoute); err != nil {
			return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
		}

		if controllerutil.RemoveFinalizer(&virtHTTPRoute, gatewayAPIFinalizerName) {
			if err := r.VirtualClient.Update(ctx, &virtHTTPRoute); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	if controllerutil.AddFinalizer(&virtHTTPRoute, gatewayAPIFinalizerName) {
		if err := r.VirtualClient.Update(ctx, &virtHTTPRoute); err != nil {
			return reconcile.Result{}, err
		}
	}

	var hostHTTPRoute gatewayv1.HTTPRoute
	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: syncedHTTPRoute.Name, Namespace: r.ClusterNamespace}, &hostHTTPRoute); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating httproute on the host cluster")
			return reconcile.Result{}, r.HostClient.Create(ctx, syncedHTTPRoute)
		}

		return reconcile.Result{}, err
	}

	log.Info("updating httproute on the host cluster")

	return reconcile.Result{}, r.HostClient.Update(ctx, syncedHTTPRoute)
}

func (r *GatewayAPIReconciler) httproute(obj *gatewayv1.HTTPRoute, syncConfig v1beta1.GatewayAPISyncConfig) *gatewayv1.HTTPRoute {
	hostHTTPRoute := obj.DeepCopy()
	r.Translator.TranslateTo(hostHTTPRoute)

	if syncConfig.OverrideParentGateway != nil {
		ns := gatewayv1.Namespace(syncConfig.OverrideParentGateway.Namespace)
		hostHTTPRoute.Spec.ParentRefs = []gatewayv1.ParentReference{{
			Name:      gatewayv1.ObjectName(syncConfig.OverrideParentGateway.Name),
			Namespace: &ns,
		}}
	} else {
		for i := range hostHTTPRoute.Spec.ParentRefs {
			ref := &hostHTTPRoute.Spec.ParentRefs[i]
			srcNS := obj.Namespace
			if ref.Namespace != nil {
				srcNS = string(*ref.Namespace)
			}
			ref.Name = gatewayv1.ObjectName(r.Translator.TranslateName(srcNS, string(ref.Name)))
			ns := gatewayv1.Namespace(r.ClusterNamespace)
			ref.Namespace = &ns
		}
	}

	for i := range hostHTTPRoute.Spec.Rules {
		for j := range hostHTTPRoute.Spec.Rules[i].BackendRefs {
			ref := &hostHTTPRoute.Spec.Rules[i].BackendRefs[j]
			ref.Name = gatewayv1.ObjectName(r.Translator.TranslateName(obj.Namespace, string(ref.Name)))
		}
	}

	return hostHTTPRoute
}
