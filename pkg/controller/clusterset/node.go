package clusterset

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	nodeController = "k3k-node-controller"
)

type NodeReconciler struct {
	Client      ctrlruntimeclient.Client
	Scheme      *runtime.Scheme
	ClusterCIDR string
}

// AddNodeController adds a new controller to the manager
func AddNodeController(ctx context.Context, mgr manager.Manager) error {
	// initialize a new Reconciler
	reconciler := NodeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Node{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Named(nodeController).
		Complete(&reconciler)
}

func (n *NodeReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("node", req.NamespacedName)
	ctx = ctrl.LoggerInto(ctx, log) // enrich the current logger

	log.Info("reconciling node")

	var clusterSetList v1alpha1.ClusterSetList
	if err := n.Client.List(ctx, &clusterSetList); err != nil {
		return reconcile.Result{}, err
	}

	if len(clusterSetList.Items) <= 0 {
		return reconcile.Result{}, nil
	}

	if err := n.ensureNetworkPolicies(ctx, clusterSetList); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (n *NodeReconciler) ensureNetworkPolicies(ctx context.Context, clusterSetList v1alpha1.ClusterSetList) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("ensuring network policies")

	var setNetworkPolicy *networkingv1.NetworkPolicy
	for _, cs := range clusterSetList.Items {
		if cs.Spec.DisableNetworkPolicy {
			continue
		}

		log = log.WithValues("clusterset", cs.Namespace+"/"+cs.Name)
		log.Info("updating NetworkPolicy for ClusterSet")

		var err error
		setNetworkPolicy, err = netpol(ctx, "", &cs, n.Client)
		if err != nil {
			return err
		}

		log.Info("new NetworkPolicy for clusterset")
		if err := n.Client.Update(ctx, setNetworkPolicy); err != nil {
			return err
		}
	}
	return nil
}
