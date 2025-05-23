package policy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	NamespacePolicyLabel = "policy.k3k.io/policy-name"
)

type NamespaceReconciler struct {
	Client      client.Client
	Scheme      *runtime.Scheme
	ClusterCIDR string
}

func addNamespaceController(mgr manager.Manager, clusterCIDR string) error {
	reconciler := &NamespaceReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterCIDR: clusterCIDR,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Namespace{}).
		Watches(
			&v1alpha1.VirtualClusterPolicy{},
			handler.EnqueueRequestsFromMapFunc(mapVCPToNamespaces(reconciler)),
		).
		Watches(
			&v1.Node{},
			handler.EnqueueRequestsFromMapFunc(mapVCPToNamespaces(reconciler)),
			builder.WithPredicates(updatePodCIDRNodePredicate),
		).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&v1.ResourceQuota{}).
		Owns(&v1alpha1.Cluster{}).
		Complete(reconciler)
}

// mapVCPToNamespaces will enqueue a reconcile request for the VirtualClusterPolicy in the given namespace
func mapVCPToNamespaces(r *VirtualClusterPolicyReconciler) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		logger := log.FromContext(ctx)

		vcp, ok := obj.(*v1alpha1.VirtualClusterPolicy)
		if !ok {
			logger.Error(fmt.Errorf("unexpected type in mapVCPToNamespaces: %T", obj), "Expected VirtualClusterPolicy")
			return []reconcile.Request{}
		}

		logger.Info("VirtualClusterPolicy changed, finding associated Namespaces", "policyName", vcp.Name)

		listOpts := client.MatchingLabels{
			NamespacePolicyLabel: vcp.Name,
		}

		var affectedNamespaces v1.NamespaceList
		if err := r.Client.List(ctx, &affectedNamespaces, listOpts); err != nil {
			logger.Error(err, "Failed to list all namespaces for VCP", "policyName", vcp.Name)
			return []reconcile.Request{}
		}

		requests := make([]reconcile.Request, len(affectedNamespaces.Items))

		for _, ns := range affectedNamespaces.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: ns.Name},
			})
		}

		return requests
	}
}

// updatePodCIDRNodePredicate trigger the Node reconciliation only on changes of the podCIDR
var updatePodCIDRNodePredicate = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*v1.Node)
		newObj := e.ObjectNew.(*v1.Node)

		// Trigger reconciliation only if the spec.podCIDR field has changed
		return oldObj.Spec.PodCIDR != newObj.Spec.PodCIDR
	},

	CreateFunc:  func(e event.CreateEvent) bool { return true },
	DeleteFunc:  func(e event.DeleteEvent) bool { return true },
	GenericFunc: func(e event.GenericEvent) bool { return true },
}

func (c *NamespaceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("clusterpolicy", req.NamespacedName)
	ctx = ctrl.LoggerInto(ctx, log) // enrich the current logger

	var namespace v1.Namespace
	if err := c.Client.Get(ctx, req.NamespacedName, &namespace); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	orig := namespace.DeepCopy()

	reconcilerErr := c.reconcile(ctx, &namespace)

	// update Status if needed
	if !reflect.DeepEqual(orig.Status, namespace.Status) {
		if err := c.Client.Status().Update(ctx, &namespace); err != nil {
			return reconcile.Result{}, err
		}
	}

	// if there was an error during the reconciliation, return
	if reconcilerErr != nil {
		return reconcile.Result{}, reconcilerErr
	}

	// update Namespace if needed

	if !reflect.DeepEqual(orig, &namespace) {
		if err := c.Client.Update(ctx, &namespace); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (c *NamespaceReconciler) reconcile(ctx context.Context, namespace *v1.Namespace) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling")

	policyName, found := namespace.GetLabels()[NamespacePolicyLabel]
	if !found {
		return c.deleteResources(ctx, namespace)
	}

	var policy v1alpha1.VirtualClusterPolicy
	if err := c.Client.Get(ctx, types.NamespacedName{Name: policyName}, &policy); err != nil {
		// TODO if not found update status
		return err
	}

	// if err := c.reconcileNamespacePodSecurityLabels(ctx, namespace, &policy); err != nil {
	// 	return err
	// }

	// if err := c.reconcileNetworkPolicy(ctx, namespace.Name, &policy); err != nil {
	// 	return err
	// }

	// if err := c.reconcileQuota(ctx, namespace.Name, &policy); err != nil {
	// 	return err
	// }

	// if err := c.reconcileLimit(ctx, namespace.Name, &policy); err != nil {
	// 	return err
	// }

	// if err := c.reconcileClusters(ctx, namespace, &policy); err != nil {
	// 	return err
	// }

	return nil
}
