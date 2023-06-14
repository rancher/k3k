package addressallocator

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	AddressAllocatorController = "address-allocator-controller"
)

type AddressAllocatorReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// Add adds a new controller to the manager
func Add(mgr manager.Manager) error {
	// initialize a new Reconciler
	reconciler := AddressAllocatorReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	controller, err := controller.New(AddressAllocatorController, mgr, controller.Options{
		Reconciler:              &reconciler,
		MaxConcurrentReconciles: 1,
	})

	if err != nil {
		return err
	}

	return controller.Watch(&source.Kind{Type: &v1alpha1.Cluster{}},
		&handler.EnqueueRequestForObject{})

}

// Reconcile will allocate cluster/service cidrs to new clusters
func (r *AddressAllocatorReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}
