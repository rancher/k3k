package policy

import (
	"context"
	"errors"
	"reflect"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	PolicyNameLabelKey          = "policy.k3k.io/policy-name"
	ManagedByLabelKey           = "app.kubernetes.io/managed-by"
	VirtualPolicyControllerName = "k3k-policy-controller"
)

type VirtualClusterPolicyReconciler struct {
	Client      client.Client
	Scheme      *runtime.Scheme
	ClusterCIDR string
}

// Add the controller to manage the Virtual Cluster policies
func Add(mgr manager.Manager, clusterCIDR string, maxConcurrentReconciles int) error {
	reconciler := VirtualClusterPolicyReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterCIDR: clusterCIDR,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VirtualClusterPolicy{}).
		Watches(&v1.Namespace{}, namespaceEventHandler()).
		Watches(&v1.Node{}, nodeEventHandler(&reconciler)).
		Watches(&v1alpha1.Cluster{}, clusterEventHandler(&reconciler)).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&v1.ResourceQuota{}).
		Owns(&v1.LimitRange{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
		Complete(&reconciler)
}

// namespaceEventHandler will enqueue a reconciliation of VCP when a Namespace changes
func namespaceEventHandler() handler.Funcs {
	return handler.Funcs{
		// When a Namespace is created, if it has the "policy.k3k.io/policy-name" label
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			ns, ok := e.Object.(*v1.Namespace)
			if !ok {
				return
			}

			if ns.Labels[PolicyNameLabelKey] != "" {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ns.Labels[PolicyNameLabelKey]}})
			}
		},
		// When a Namespace is updated, if it has the "policy.k3k.io/policy-name" label
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			oldNs, okOld := e.ObjectOld.(*v1.Namespace)
			newNs, okNew := e.ObjectNew.(*v1.Namespace)

			if !okOld || !okNew {
				return
			}

			oldVCPName := oldNs.Labels[PolicyNameLabelKey]
			newVCPName := newNs.Labels[PolicyNameLabelKey]

			// If labels haven't changed we can skip the reconciliation
			if reflect.DeepEqual(oldNs.Labels, newNs.Labels) {
				return
			}

			// If No VCP before and after we can skip the reconciliation
			if oldVCPName == "" && newVCPName == "" {
				return
			}

			// The VCP has not changed, but we enqueue a reconciliation because the PSA or other labels have changed
			if oldVCPName == newVCPName {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: oldVCPName}})
				return
			}

			// Enqueue the old VCP name for cleanup
			if oldVCPName != "" {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: oldVCPName}})
			}

			// Enqueue the new VCP name
			if newVCPName != "" {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: newVCPName}})
			}
		},
		// When a namespace is deleted all the resources in the namespace are deleted
		// but we trigger the reconciliation to eventually perform some cluster-wide cleanup if necessary
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			ns, ok := e.Object.(*v1.Namespace)
			if !ok {
				return
			}

			if ns.Labels[PolicyNameLabelKey] != "" {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ns.Labels[PolicyNameLabelKey]}})
			}
		},
	}
}

// nodeEventHandler will enqueue a reconciliation of all the VCPs when a Node changes.
// This happens only if the ClusterCIDR is NOT specified, to handle the PodCIDRs in the NetworkPolicies.
func nodeEventHandler(r *VirtualClusterPolicyReconciler) handler.Funcs {
	// enqueue all the available VirtualClusterPolicies
	enqueueAllVCPs := func(ctx context.Context, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		vcpList := &v1alpha1.VirtualClusterPolicyList{}
		if err := r.Client.List(ctx, vcpList); err != nil {
			return
		}

		for _, vcp := range vcpList.Items {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: vcp.Name}})
		}
	}

	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if r.ClusterCIDR != "" {
				return
			}

			enqueueAllVCPs(ctx, q)
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if r.ClusterCIDR != "" {
				return
			}

			oldNode, okOld := e.ObjectOld.(*v1.Node)
			newNode, okNew := e.ObjectNew.(*v1.Node)

			if !okOld || !okNew {
				return
			}

			// Check if PodCIDR or PodCIDRs fields have changed.

			var podCIDRChanged bool
			if oldNode.Spec.PodCIDR != newNode.Spec.PodCIDR {
				podCIDRChanged = true
			}
			if !reflect.DeepEqual(oldNode.Spec.PodCIDRs, newNode.Spec.PodCIDRs) {
				podCIDRChanged = true
			}

			if podCIDRChanged {
				enqueueAllVCPs(ctx, q)
			}
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if r.ClusterCIDR != "" {
				return
			}

			enqueueAllVCPs(ctx, q)
		},
	}
}

// clusterEventHandler will enqueue a reconciliation of the VCP associated to the Namespace when a Cluster changes.
func clusterEventHandler(r *VirtualClusterPolicyReconciler) handler.Funcs {
	type clusterSubSpec struct {
		PriorityClass string
		NodeSelector  map[string]string
	}

	return handler.Funcs{
		// When a Cluster is created, if its Namespace has the "policy.k3k.io/policy-name" label
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			cluster, ok := e.Object.(*v1alpha1.Cluster)
			if !ok {
				return
			}

			var ns v1.Namespace
			if err := r.Client.Get(ctx, types.NamespacedName{Name: cluster.Namespace}, &ns); err != nil {
				return
			}

			if ns.Labels[PolicyNameLabelKey] != "" {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ns.Labels[PolicyNameLabelKey]}})
			}
		},
		// When a Cluster is updated, if its Namespace has the "policy.k3k.io/policy-name" label
		// and if some of its spec influenced by the policy changed
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			oldCluster, okOld := e.ObjectOld.(*v1alpha1.Cluster)
			newCluster, okNew := e.ObjectNew.(*v1alpha1.Cluster)

			if !okOld || !okNew {
				return
			}

			var ns v1.Namespace
			if err := r.Client.Get(ctx, types.NamespacedName{Name: oldCluster.Namespace}, &ns); err != nil {
				return
			}

			if ns.Labels[PolicyNameLabelKey] == "" {
				return
			}

			clusterSubSpecOld := clusterSubSpec{
				PriorityClass: oldCluster.Spec.PriorityClass,
				NodeSelector:  oldCluster.Spec.NodeSelector,
			}

			clusterSubSpecNew := clusterSubSpec{
				PriorityClass: newCluster.Spec.PriorityClass,
				NodeSelector:  newCluster.Spec.NodeSelector,
			}

			if !reflect.DeepEqual(clusterSubSpecOld, clusterSubSpecNew) {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ns.Labels[PolicyNameLabelKey]}})
			}
		},
		// When a Cluster is deleted -> nothing to do
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		},
	}
}

func (c *VirtualClusterPolicyReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling VirtualClusterPolicy")

	var policy v1alpha1.VirtualClusterPolicy
	if err := c.Client.Get(ctx, req.NamespacedName, &policy); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	orig := policy.DeepCopy()

	reconcilerErr := c.reconcileVirtualClusterPolicy(ctx, &policy)

	// update Status if needed
	if !reflect.DeepEqual(orig.Status, policy.Status) {
		if err := c.Client.Status().Update(ctx, &policy); err != nil {
			return reconcile.Result{}, err
		}
	}

	// if there was an error during the reconciliation, return
	if reconcilerErr != nil {
		return reconcile.Result{}, reconcilerErr
	}

	// update VirtualClusterPolicy if needed
	if !reflect.DeepEqual(orig, policy) {
		if err := c.Client.Update(ctx, &policy); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (c *VirtualClusterPolicyReconciler) reconcileVirtualClusterPolicy(ctx context.Context, policy *v1alpha1.VirtualClusterPolicy) error {
	if err := c.reconcileMatchingNamespaces(ctx, policy); err != nil {
		return err
	}

	if err := c.cleanupNamespaces(ctx); err != nil {
		return err
	}

	return nil
}

func (c *VirtualClusterPolicyReconciler) reconcileMatchingNamespaces(ctx context.Context, policy *v1alpha1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling matching Namespaces")

	listOpts := client.MatchingLabels{
		PolicyNameLabelKey: policy.Name,
	}

	var namespaces v1.NamespaceList
	if err := c.Client.List(ctx, &namespaces, listOpts); err != nil {
		return err
	}

	for _, ns := range namespaces.Items {
		ctx = ctrl.LoggerInto(ctx, log.WithValues("namespace", ns.Name))
		log.Info("reconciling Namespace")

		orig := ns.DeepCopy()

		if err := c.reconcileNetworkPolicy(ctx, ns.Name, policy); err != nil {
			return err
		}

		if err := c.reconcileQuota(ctx, ns.Name, policy); err != nil {
			return err
		}

		if err := c.reconcileLimit(ctx, ns.Name, policy); err != nil {
			return err
		}

		if err := c.reconcileClusters(ctx, &ns, policy); err != nil {
			return err
		}

		c.reconcileNamespacePodSecurityLabels(ctx, &ns, policy)

		if !reflect.DeepEqual(orig, &ns) {
			if err := c.Client.Update(ctx, &ns); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *VirtualClusterPolicyReconciler) reconcileQuota(ctx context.Context, namespace string, policy *v1alpha1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling ResourceQuota")

	if policy.Spec.Quota == nil {
		// check if resourceQuota object exists and deletes it.
		var toDeleteResourceQuota v1.ResourceQuota

		key := types.NamespacedName{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespace,
		}

		if err := c.Client.Get(ctx, key, &toDeleteResourceQuota); err != nil {
			return client.IgnoreNotFound(err)
		}

		return c.Client.Delete(ctx, &toDeleteResourceQuota)
	}

	// create/update resource Quota
	resourceQuota := &v1.ResourceQuota{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ResourceQuota",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespace,
			Labels: map[string]string{
				ManagedByLabelKey:  VirtualPolicyControllerName,
				PolicyNameLabelKey: policy.Name,
			},
		},
		Spec: *policy.Spec.Quota,
	}

	if err := ctrl.SetControllerReference(policy, resourceQuota, c.Scheme); err != nil {
		return err
	}

	err := c.Client.Create(ctx, resourceQuota)
	if apierrors.IsAlreadyExists(err) {
		return c.Client.Update(ctx, resourceQuota)
	}

	return err
}

func (c *VirtualClusterPolicyReconciler) reconcileLimit(ctx context.Context, namespace string, policy *v1alpha1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling LimitRange")

	// delete limitrange if spec.limits isnt specified.
	if policy.Spec.Limit == nil {
		var toDeleteLimitRange v1.LimitRange

		key := types.NamespacedName{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespace,
		}

		if err := c.Client.Get(ctx, key, &toDeleteLimitRange); err != nil {
			return client.IgnoreNotFound(err)
		}

		return c.Client.Delete(ctx, &toDeleteLimitRange)
	}

	limitRange := &v1.LimitRange{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LimitRange",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespace,
			Labels: map[string]string{
				ManagedByLabelKey:  VirtualPolicyControllerName,
				PolicyNameLabelKey: policy.Name,
			},
		},
		Spec: *policy.Spec.Limit,
	}

	if err := ctrl.SetControllerReference(policy, limitRange, c.Scheme); err != nil {
		return err
	}

	err := c.Client.Create(ctx, limitRange)
	if apierrors.IsAlreadyExists(err) {
		return c.Client.Update(ctx, limitRange)
	}

	return err
}

func (c *VirtualClusterPolicyReconciler) reconcileClusters(ctx context.Context, namespace *v1.Namespace, policy *v1alpha1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling Clusters")

	var clusters v1alpha1.ClusterList
	if err := c.Client.List(ctx, &clusters, client.InNamespace(namespace.Name)); err != nil {
		return err
	}

	var clusterUpdateErrs []error

	for _, cluster := range clusters.Items {
		orig := cluster.DeepCopy()

		cluster.Spec.PriorityClass = policy.Spec.DefaultPriorityClass
		cluster.Spec.NodeSelector = policy.Spec.DefaultNodeSelector

		if !reflect.DeepEqual(orig, cluster) {
			// continue updating also the other clusters even if an error occurred
			clusterUpdateErrs = append(clusterUpdateErrs, c.Client.Update(ctx, &cluster))
		}
	}

	return errors.Join(clusterUpdateErrs...)
}
