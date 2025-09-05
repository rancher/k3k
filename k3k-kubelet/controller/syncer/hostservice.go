package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
)

const (
	hostServiceControllerName = "host-service-status-syncer-controller"
)

type HostServiceStatusReconciler struct {
	*SyncerContext
}

// AddHostServiceStatusSyncer adds host service syncer to sync back LB status to the virtual service.
func AddHostServiceStatusSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}

	reconciler := HostServiceStatusReconciler{
		SyncerContext: &SyncerContext{
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
			VirtualClient:    virtMgr.GetClient(),
			HostClient:       hostMgr.GetClient(),
			Translator:       translator,
		},
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, hostServiceControllerName)

	return ctrl.NewControllerManagedBy(hostMgr).
		Named(name).
		For(&v1.Service{}).WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *HostServiceStatusReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	var (
		virtServiceName, virtServiceNamespace string
		hostService, virtService              v1.Service
	)

	if err := r.HostClient.Get(ctx, req.NamespacedName, &hostService); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	// ignoring the service if annotation is empty or resoruce Name/Namespace is not assigned
	if hostService.Annotations == nil {
		return reconcile.Result{}, nil
	}

	virtServiceName = hostService.Annotations[translate.ResourceNameAnnotation]
	virtServiceNamespace = hostService.Annotations[translate.ResourceNamespaceAnnotation]

	if virtServiceName == "" || virtServiceNamespace == "" {
		return reconcile.Result{}, nil
	}

	if !hostService.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	if err := r.VirtualClient.Get(ctx, types.NamespacedName{Name: virtServiceName, Namespace: virtServiceNamespace}, &virtService); err != nil {
		return reconcile.Result{}, err
	}

	if !equality.Semantic.DeepEqual(virtService.Status.LoadBalancer, hostService.Status.LoadBalancer) {
		virtService.Status.LoadBalancer = hostService.Status.LoadBalancer
		if err := r.VirtualClient.Status().Update(ctx, &virtService); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *HostServiceStatusReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1alpha1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	ownerRefernces := object.GetOwnerReferences()
	for _, owner := range ownerRefernces {
		if owner.Kind == "Cluster" && owner.Name == cluster.Name {
			return true
		}
	}

	return false
}
