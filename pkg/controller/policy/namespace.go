package policy

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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

func AddNSReconciler(mgr manager.Manager, clusterCIDR string) error {
	// initialize a new Reconciler
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
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&v1.ResourceQuota{}).
		Owns(&v1alpha1.Cluster{}).
		Complete(reconciler)
}

// namespaceEventHandler will enqueue a reconcile request for the VirtualClusterPolicy in the given namespace
func mapVCPToNamespaces(r *NamespaceReconciler) handler.MapFunc {
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

	if err := reconcileNamespace(ctx, c.Client, c.Scheme, namespace, &policy, c.ClusterCIDR); err != nil {
		return err
	}

	return nil
}

func reconcileNamespace(ctx context.Context, c client.Client, scheme *runtime.Scheme, namespace *v1.Namespace, policy *v1alpha1.VirtualClusterPolicy, clusterCIDR string) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling")

	if err := reconcileNamespacePodSecurityLabels(ctx, c, namespace, policy); err != nil {
		return err
	}

	if err := reconcileNetworkPolicy(ctx, c, scheme, namespace, policy, clusterCIDR); err != nil {
		return err
	}

	if err := reconcileQuota(ctx, c, scheme, namespace, policy); err != nil {
		return err
	}

	if err := reconcileClusters(ctx, c, scheme, namespace, policy); err != nil {
		return err
	}

	return nil
}

func reconcileNetworkPolicy(ctx context.Context, c client.Client, scheme *runtime.Scheme, namespace *v1.Namespace, policy *v1alpha1.VirtualClusterPolicy, clusterCIDR string) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling NetworkPolicy")

	networkPolicy, err := netpol(ctx, c, namespace.Name, policy, clusterCIDR)
	if err != nil {
		return err
	}

	if err = ctrl.SetControllerReference(namespace, networkPolicy, scheme); err != nil {
		return err
	}

	// if disabled then delete the existing network policy
	if policy.Spec.DisableNetworkPolicy {
		err := c.Delete(ctx, networkPolicy)
		return client.IgnoreNotFound(err)
	}

	// otherwise try to create/update
	err = c.Create(ctx, networkPolicy)
	if apierrors.IsAlreadyExists(err) {
		return c.Update(ctx, networkPolicy)
	}

	return err
}

func reconcileNamespacePodSecurityLabels(ctx context.Context, c client.Client, namespace *v1.Namespace, policy *v1alpha1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling Namespace")

	// cleanup of old labels
	delete(namespace.Labels, "pod-security.kubernetes.io/enforce")
	delete(namespace.Labels, "pod-security.kubernetes.io/enforce-version")
	delete(namespace.Labels, "pod-security.kubernetes.io/warn")
	delete(namespace.Labels, "pod-security.kubernetes.io/warn-version")

	// if a PSA level is specified add the proper labels
	if policy.Spec.PodSecurityAdmissionLevel != nil {
		psaLevel := *policy.Spec.PodSecurityAdmissionLevel

		namespace.Labels["pod-security.kubernetes.io/enforce"] = string(psaLevel)
		namespace.Labels["pod-security.kubernetes.io/enforce-version"] = "latest"

		// skip the 'warn' only for the privileged PSA level
		if psaLevel != v1alpha1.PrivilegedPodSecurityAdmissionLevel {
			namespace.Labels["pod-security.kubernetes.io/warn"] = string(psaLevel)
			namespace.Labels["pod-security.kubernetes.io/warn-version"] = "latest"
		}
	}

	return nil
}

func reconcileQuota(ctx context.Context, c client.Client, scheme *runtime.Scheme, namespace *v1.Namespace, policy *v1alpha1.VirtualClusterPolicy) error {
	if policy.Spec.Quota == nil {
		// check if resourceQuota object exists and deletes it.
		var toDeleteResourceQuota v1.ResourceQuota

		key := types.NamespacedName{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespace.Name,
		}

		if err := c.Get(ctx, key, &toDeleteResourceQuota); err != nil {
			return client.IgnoreNotFound(err)
		}

		return c.Delete(ctx, &toDeleteResourceQuota)
	}

	// create/update resource Quota
	resourceQuota := &v1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespace.Name,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "ResourceQuota",
			APIVersion: "v1",
		},
		Spec: *policy.Spec.Quota,
	}

	if err := ctrl.SetControllerReference(namespace, resourceQuota, scheme); err != nil {
		return err
	}

	if err := c.Create(ctx, resourceQuota); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return c.Update(ctx, resourceQuota)
		}
	}

	return nil
}

func reconcileClusters(ctx context.Context, c client.Client, scheme *runtime.Scheme, namespace *v1.Namespace, policy *v1alpha1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling Clusters")

	var clusters v1alpha1.ClusterList
	if err := c.List(ctx, &clusters, client.InNamespace(namespace.Name)); err != nil {
		return err
	}

	var err error

	for _, cluster := range clusters.Items {
		oldClusterSpec := cluster.Spec

		if cluster.Spec.PriorityClass != policy.Spec.DefaultPriorityClass {
			cluster.Spec.PriorityClass = policy.Spec.DefaultPriorityClass
		}

		if !reflect.DeepEqual(cluster.Spec.NodeSelector, policy.Spec.DefaultNodeSelector) {
			cluster.Spec.NodeSelector = policy.Spec.DefaultNodeSelector
		}

		if err := ctrl.SetControllerReference(namespace, &cluster, scheme); err != nil {
			return err
		}

		if !reflect.DeepEqual(oldClusterSpec, cluster.Spec) {
			// continue updating also the other clusters even if an error occurred
			// TODO add status?
			err = errors.Join(c.Update(ctx, &cluster))
		}
	}

	return err
}

func (c *NamespaceReconciler) deleteResources(ctx context.Context, namespace *v1.Namespace) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("deleting resources")

	deleteOpts := []client.DeleteAllOfOption{
		client.InNamespace(namespace.Name),
		client.MatchingLabels{
			"app.kubernetes.io/managed-by": "k3k-policy-controller",
		},
	}

	if err := c.Client.DeleteAllOf(ctx, &v1.ResourceQuota{}, deleteOpts...); err != nil {
		return err
	}

	if err := c.Client.DeleteAllOf(ctx, &v1.LimitRange{}, deleteOpts...); err != nil {
		return err
	}

	return nil
}
