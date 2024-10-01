package clusterset

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
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

// Add adds a new controller to the manager
func AddNodeController(ctx context.Context, mgr manager.Manager, clusterCIDR string) error {
	// initialize a new Reconciler
	reconciler := NodeReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterCIDR: clusterCIDR,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Node{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(&reconciler)
}

func (n *NodeReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if n.ClusterCIDR != "" {
		return reconcile.Result{}, nil
	}

	var clusterSetList v1alpha1.ClusterSetList
	if err := n.Client.List(ctx, &clusterSetList); err != nil {
		return reconcile.Result{}, util.LogAndReturnErr("failed to list clusterSets", err)
	}

	if len(clusterSetList.Items) <= 0 {
		return reconcile.Result{}, nil
	}

	var node v1.Node
	if err := n.Client.Get(ctx, types.NamespacedName{Name: req.Name}, &node); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, util.LogAndReturnErr("failed to handle get node", err)
		}
	}
	if node.DeletionTimestamp.IsZero() {
		for _, cs := range clusterSetList.Items {
			klog.Infof("Add new CIDR %v of node %s to clusterSet %s networkpolicy", node.Spec.PodCIDR, req.Name, cs.Name)
			if err := n.addCIDR(ctx, cs, node.Spec.PodCIDR); err != nil {
				return reconcile.Result{}, util.LogAndReturnErr("failed to update networkpolicy with new node cidr", err)
			}
		}

		return reconcile.Result{}, nil
	}

	for _, cs := range clusterSetList.Items {
		klog.Infof("Remove CIDR %v of node %s from clusterSet %s networkpolicy", node.Spec.PodCIDR, req.Name, cs.Name)
		if err := n.removeCIDR(ctx, cs, node.Spec.PodCIDR); err != nil {
			return reconcile.Result{}, util.LogAndReturnErr("failed to remove cidr from networkpolicy", err)
		}
	}
	return reconcile.Result{}, nil
}

func (n *NodeReconciler) addCIDR(ctx context.Context, cs v1alpha1.ClusterSet, podCIDR string) error {
	var netPolicy networkingv1.NetworkPolicy
	if err := n.Client.Get(ctx, types.NamespacedName{Name: networkPolicyName, Namespace: cs.Namespace}, &netPolicy); err != nil {
		return err
	}
	if len(netPolicy.Spec.Egress) > 0 {
		if len(netPolicy.Spec.Egress[0].To) > 0 {
			if netPolicy.Spec.Egress[0].To[0].IPBlock != nil {
				for _, existingCIDR := range netPolicy.Spec.Egress[0].To[0].IPBlock.Except {
					if existingCIDR == podCIDR {
						return nil
					}
				}

				netPolicy.Spec.Egress[0].To[0].IPBlock.Except = append(netPolicy.Spec.Egress[0].To[0].IPBlock.Except, podCIDR)
				if err := n.Client.Update(ctx, &netPolicy); err != nil {
					return err
				}
			}
		}

	}
	return nil
}

func (n *NodeReconciler) removeCIDR(ctx context.Context, cs v1alpha1.ClusterSet, podCIDR string) error {
	var netPolicy networkingv1.NetworkPolicy
	if err := n.Client.Get(ctx, types.NamespacedName{Name: networkPolicyName, Namespace: cs.Namespace}, &netPolicy); err != nil {
		return err
	}
	if len(netPolicy.Spec.Egress) > 0 {
		if len(netPolicy.Spec.Egress[0].To) > 0 {
			if netPolicy.Spec.Egress[0].To[0].IPBlock != nil {
				for i, existingCIDR := range netPolicy.Spec.Egress[0].To[0].IPBlock.Except {
					if existingCIDR == podCIDR {
						netPolicy.Spec.Egress[0].To[0].IPBlock.Except = append(netPolicy.Spec.Egress[0].To[0].IPBlock.Except[:i], netPolicy.Spec.Egress[0].To[0].IPBlock.Except[i+1:]...)
						if err := n.Client.Update(ctx, &netPolicy); err != nil {
							return err
						}
					}
				}
			}
		}

	}
	return nil
}
