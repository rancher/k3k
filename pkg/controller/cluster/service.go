package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
		For(&v1.Service{}).
		WithEventFilter(newClusterPredicate()).
		Complete(&reconciler)
}

func (r *ServiceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("ensuring service status to virtual cluster")

	var hostService v1.Service
	if err := r.HostClient.Get(ctx, req.NamespacedName, &hostService); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	// Some services are owned by the cluster but don't have the annotations set (i.e. the kubelet svc)
	// They don't exists in the virtual cluster, so we can skip them

	virtualServiceName, virtualServiceNameFound := hostService.Annotations[translate.ResourceNameAnnotation]
	virtualServiceNamespace, virtualServiceNamespaceFound := hostService.Annotations[translate.ResourceNamespaceAnnotation]

	if !virtualServiceNameFound || !virtualServiceNamespaceFound {
		log.V(1).Info(fmt.Sprintf("service %s/%s does not have virtual service annotations, skipping", hostService.Namespace, hostService.Name))
		return reconcile.Result{}, nil
	}

	// get cluster from the object
	cluster := clusterNamespacedName(&hostService)

	virtualClient, err := newVirtualClient(ctx, r.HostClient, cluster.Name, cluster.Namespace)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get cluster info: %v", err)
	}

	if !hostService.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	virtualServiceKey := types.NamespacedName{
		Name:      virtualServiceName,
		Namespace: virtualServiceNamespace,
	}

	var virtualService v1.Service
	if err := virtualClient.Get(ctx, virtualServiceKey, &virtualService); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get virtual service: %v", err)
	}

	if !equality.Semantic.DeepEqual(virtualService.Status.LoadBalancer, hostService.Status.LoadBalancer) {
		virtualService.Status.LoadBalancer = hostService.Status.LoadBalancer
		if err := virtualClient.Status().Update(ctx, &virtualService); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}
