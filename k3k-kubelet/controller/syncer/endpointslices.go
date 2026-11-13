package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	endpointSliceControllerName           = "endpointslice-syncer-controller"
	kubernetesEndpointSliceControllerName = "endpointslice-controller.k8s.io"
	endpointSliceFinalizerName            = "endpointslice.k3k.io/finalizer"
)

// EndpointSliceReconciler syncs the EndpointSlices of the virtual cluster to the host cluster.
type EndpointSliceReconciler struct {
	*Context
}

// AddEndpointSliceSyncer adds endpointSlices syncer controller to the manager of the virtual cluster
func AddEndpointSliceSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}

	reconciler := EndpointSliceReconciler{
		Context: &Context{
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
			VirtualClient:    virtMgr.GetClient(),
			HostClient:       hostMgr.GetClient(),
			Translator:       translator,
		},
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, endpointSliceControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&discoveryv1.EndpointSlice{}).WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

// Reconcile creates, updates or deletes the host EndpointSlice matching a virtual one.
func (r *EndpointSliceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	var (
		virtEndpointSlice discoveryv1.EndpointSlice
		cluster           v1beta1.Cluster
	)

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.VirtualClient.Get(ctx, req.NamespacedName, &virtEndpointSlice); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedEndpointSlice := r.endpointSlice(&virtEndpointSlice)

	if err := controllerutil.SetOwnerReference(&cluster, syncedEndpointSlice, r.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtEndpointSlice.DeletionTimestamp.IsZero() {
		// deleting the synced endpointSlice if exists
		if err := r.HostClient.Delete(ctx, syncedEndpointSlice); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced endpointSlice
		if controllerutil.RemoveFinalizer(&virtEndpointSlice, endpointSliceFinalizerName) {
			if err := r.VirtualClient.Update(ctx, &virtEndpointSlice); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtEndpointSlice, endpointSliceFinalizerName) {
		if err := r.VirtualClient.Update(ctx, &virtEndpointSlice); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create or update the endpointSlice on host
	var hostEndpointSlice discoveryv1.EndpointSlice
	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: syncedEndpointSlice.Name, Namespace: r.ClusterNamespace}, &hostEndpointSlice); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the endpointSlice for the first time on the host cluster")
			return reconcile.Result{}, r.HostClient.Create(ctx, syncedEndpointSlice)
		}

		return reconcile.Result{}, err
	}

	log.Info("updating EndpointSlice on the host cluster")

	syncedEndpointSlice.ResourceVersion = hostEndpointSlice.ResourceVersion

	return reconcile.Result{}, r.HostClient.Update(ctx, syncedEndpointSlice)
}

func (r *EndpointSliceReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1beta1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	appliedSync := cluster.Spec.Sync.DeepCopy()

	// If a policy is applied to the virtual cluster we need to use its SyncConfig, if available
	if cluster.Status.Policy != nil && cluster.Status.Policy.Sync != nil {
		appliedSync = cluster.Status.Policy.Sync
	}

	epSyncConfig := appliedSync.EndpointSlices
	serviceSyncConfig := appliedSync.Services

	// If syncing for endpointSlices or services is disabled, only process deletions to allow for cleanup.
	if !epSyncConfig.Enabled || !serviceSyncConfig.Enabled {
		return object.GetDeletionTimestamp() != nil
	}

	if object.GetName() == "kubernetes" {
		return false
	}

	// only handle endpointSlices that are not managed by the endpointSlices controller
	if val := object.GetLabels()[discoveryv1.LabelManagedBy]; val == kubernetesEndpointSliceControllerName {
		return false
	}

	if object.GetLabels()[discoveryv1.LabelServiceName] == "" {
		return false
	}

	labelSelector := labels.SelectorFromSet(epSyncConfig.Selector)
	if labelSelector.Empty() {
		return true
	}

	return labelSelector.Matches(labels.Set(object.GetLabels()))
}

func (r *EndpointSliceReconciler) endpointSlice(obj *discoveryv1.EndpointSlice) *discoveryv1.EndpointSlice {
	hostEndpointSlice := obj.DeepCopy()
	r.Translator.TranslateTo(hostEndpointSlice)

	if serviceName, ok := obj.GetLabels()[discoveryv1.LabelServiceName]; ok && serviceName != "" {
		hostEndpointSlice.Labels[discoveryv1.LabelServiceName] = r.Translator.TranslateName(obj.Namespace, serviceName)
	}

	// translate targetRef if exists
	for i := range hostEndpointSlice.Endpoints {
		if hostEndpointSlice.Endpoints[i].TargetRef == nil {
			continue
		}

		targetRef := hostEndpointSlice.Endpoints[i].TargetRef
		hostEndpointSlice.Endpoints[i].TargetRef = &corev1.ObjectReference{
			Kind:      targetRef.Kind,
			Name:      r.Translator.TranslateName(targetRef.Namespace, targetRef.Name),
			Namespace: hostEndpointSlice.Namespace,
		}
	}

	return hostEndpointSlice
}
