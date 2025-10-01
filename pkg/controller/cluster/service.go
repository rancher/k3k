package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

const (
	serviceController = "k3k-service-controller"
)

type ServiceReconciler struct {
	HostClient ctrlruntimeclient.Client
}

// Add adds a new controller to the manager
func AddServiceController(ctx context.Context, mgr manager.Manager, maxConcurrentReconciles int) error {
	reconciler := ServiceReconciler{
		HostClient: mgr.GetClient(),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named(serviceController).
		For(&v1.Service{}).WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *ServiceReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	_, hasClusterNameLabel := object.GetLabels()[translate.ClusterNameLabel]
	_, hasNameAnnotation := object.GetAnnotations()[translate.ResourceNameAnnotation]
	_, hasNamespaceAnnotation := object.GetAnnotations()[translate.ResourceNamespaceAnnotation]

	// Return true only if all were found
	return hasClusterNameLabel && hasNameAnnotation && hasNamespaceAnnotation
}

func (r *ServiceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("ensuring service status to virtual cluster")

	var (
		virtServiceName, virtServiceNamespace string
		hostService, virtService              v1.Service
	)

	if err := r.HostClient.Get(ctx, req.NamespacedName, &hostService); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	// get cluster information from the object
	labels := hostService.GetLabels()

	clusterName, ok := labels[translate.ClusterNameLabel]
	if !ok {
		return reconcile.Result{}, fmt.Errorf("cluster name label is not found")
	}

	clusterNamespace := hostService.GetNamespace()

	virtualClient, err := newVirtualClient(ctx, r.HostClient, clusterName, clusterNamespace)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get cluster info: %v", err)
	}

	virtServiceName = hostService.Annotations[translate.ResourceNameAnnotation]
	virtServiceNamespace = hostService.Annotations[translate.ResourceNamespaceAnnotation]

	if !hostService.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	if err := virtualClient.Get(ctx, types.NamespacedName{Name: virtServiceName, Namespace: virtServiceNamespace}, &virtService); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get virt service: %v", err)
	}

	if !equality.Semantic.DeepEqual(virtService.Status.LoadBalancer, hostService.Status.LoadBalancer) {
		virtService.Status.LoadBalancer = hostService.Status.LoadBalancer
		if err := virtualClient.Status().Update(ctx, &virtService); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}
