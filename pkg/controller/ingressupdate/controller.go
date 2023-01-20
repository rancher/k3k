package ingressupdate

import (
	"context"

	"github.com/galal-hussein/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	IngressUpdateController = "ingress-update-controller"
)

type IngressUpdateReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// Add adds a new controller to the manager
func Add(mgr manager.Manager) error {
	// initialize a new Reconciler
	reconciler := IngressUpdateReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	controller, err := controller.New(IngressUpdateController, mgr, controller.Options{
		Reconciler:              &reconciler,
		MaxConcurrentReconciles: 1,
	})

	if err != nil {
		return err
	}

	return controller.Watch(&source.Kind{Type: &v1.Node{}}, &handler.EnqueueRequestForObject{})

}

// Reconcile will update ingresses each time a node added/deleted/changes with the new addresses
func (r *IngressUpdateReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {

	nodeList := v1.NodeList{}

	if err := r.Client.List(ctx, &nodeList); err != nil {
		return reconcile.Result{}, util.WrapErr("failed to list nodes", err)
	}

	// if err := r.Client.Get(ctx, req.NamespacedName, &cluster); err != nil {
	// 	return reconcile.Result{}, util.WrapErr(fmt.Sprintf("failed to get cluster %s", req.NamespacedName), err)
	// }

	// // we create a namespace for each new cluster
	// ns := &v1.Namespace{}
	// if err := r.Client.Get(ctx, client.ObjectKey{Name: util.ClusterNamespace(&cluster)}, ns); err != nil {
	// 	if apierrors.IsNotFound(err) {
	// 		klog.Infof("creating new cluster")

	// 		return reconcile.Result{}, r.createCluster(ctx, &cluster)
	// 	} else {
	// 		return reconcile.Result{},
	// 			util.WrapErr(fmt.Sprintf("failed to get cluster namespace %s", util.ClusterNamespace(&cluster)), err)
	// 	}
	// }

	return reconcile.Result{}, nil
}
