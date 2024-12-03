package clusterset

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/log"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const nodeController = "k3k-node-controller"

type NodeReconciler struct {
	Client      ctrlruntimeclient.Client
	Scheme      *runtime.Scheme
	ClusterCIDR string
	logger      *log.Logger
}

// AddNodeController adds a new controller to the manager
func AddNodeController(ctx context.Context, mgr manager.Manager, logger *log.Logger) error {
	// initialize a new Reconciler
	reconciler := NodeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		logger: logger.Named(nodeController),
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
	log := n.logger.With("Node", req.NamespacedName)
	var clusterSetList v1alpha1.ClusterSetList
	if err := n.Client.List(ctx, &clusterSetList); err != nil {
		return reconcile.Result{}, err
	}

	if len(clusterSetList.Items) <= 0 {
		return reconcile.Result{}, nil
	}

	if err := n.ensureNetworkPolicies(ctx, clusterSetList, log); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (n *NodeReconciler) ensureNetworkPolicies(ctx context.Context, clusterSetList v1alpha1.ClusterSetList, log *zap.SugaredLogger) error {
	for _, cs := range clusterSetList.Items {
		if cs.Spec.DisableNetworkPolicy {
			continue
		}

		log.Infow("Updating NetworkPolicy for ClusterSet", "name", cs.Name, "namespace", cs.Namespace)
		setNetworkPolicy, err := netpol(ctx, "", &cs, n.Client)
		if err != nil {
			return err
		}
		log.Debugw("New NetworkPolicy for clusterset", "name", cs.Name, "namespace", cs.Namespace)
		if err := n.Client.Update(ctx, setNetworkPolicy); err != nil {
			return err
		}
	}
	return nil
}
