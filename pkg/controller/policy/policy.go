package policy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	VCPLabelKey                 = "policy.k3k.io/policy-name"
	ManagedByLabelKey           = "app.kubernetes.io/managed-by"
	VirtualPolicyControllerName = "k3k-policy-controller"
)

type VirtualClusterPolicyReconciler struct {
	Client      client.Client
	Scheme      *runtime.Scheme
	ClusterCIDR string
}

// Add the controller to manage the Virtual Cluster policies
func Add(mgr manager.Manager, clusterCIDR string) error {
	reconciler := VirtualClusterPolicyReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterCIDR: clusterCIDR,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VirtualClusterPolicy{}).
		Watches(&v1.Namespace{}, namespaceEventHandler()).
		Watches(&v1.Node{}, nodeEventHandler(&reconciler)).
		Watches(&v1alpha1.Cluster{}, nodeEventHandler(&reconciler)).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&v1.ResourceQuota{}).
		Complete(&reconciler)
}

func namespaceEventHandler() handler.Funcs {
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
			ns, ok := e.Object.(*v1.Namespace)
			if !ok {
				return
			}

			if ns.Labels[VCPLabelKey] != "" {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ns.Labels[VCPLabelKey]}})
			}

		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
			oldNs, okOld := e.ObjectOld.(*v1.Namespace)
			newNs, okNew := e.ObjectNew.(*v1.Namespace)

			if !okOld || !okNew {
				return
			}

			oldVCPName := oldNs.Labels[VCPLabelKey]
			newVCPName := newNs.Labels[VCPLabelKey]

			// if labels haven't changed -> skip reconciliation
			if reflect.DeepEqual(oldNs.Labels, newNs.Labels) {
				return
			}

			fmt.Println("labels changed!", "oldVCPName", oldVCPName, "newVCPName", newVCPName)

			// If the VCP have not changed we need to check if labels changed
			if oldVCPName == newVCPName {
				if oldVCPName != "" {
					q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: oldVCPName}})
				}
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
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
			ns, ok := e.Object.(*v1.Namespace)
			if !ok {
				return
			}

			// When a namespace is deleted all the resources in the namespace are deleted
			// but we trigger the reconciliation to eventually perform some cluster-wide cleanup if necessary
			if ns.Labels[VCPLabelKey] != "" {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ns.Labels[VCPLabelKey]}})
			}
		},
	}
}

func nodeEventHandler(r *VirtualClusterPolicyReconciler) handler.Funcs {
	// Helper function in your VirtualClusterPolicyReconciler to enqueue VCPs
	enqueueAllVCPs := func(ctx context.Context, q workqueue.RateLimitingInterface) {
		vcpList := &v1alpha1.VirtualClusterPolicyList{}
		if err := r.Client.List(ctx, vcpList); err != nil {
			return
		}

		for _, vcp := range vcpList.Items {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: vcp.Name}})
		}
	}

	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
			enqueueAllVCPs(ctx, q)
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
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
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
			enqueueAllVCPs(ctx, q)
		},
	}
}

func (c *VirtualClusterPolicyReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("clusterpolicy", req.NamespacedName)
	ctx = ctrl.LoggerInto(ctx, log)

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
	listOpts := client.MatchingLabels{
		NamespacePolicyLabel: policy.Name,
	}

	var namespaces v1.NamespaceList
	if err := c.Client.List(ctx, &namespaces, listOpts); err != nil {
		return err
	}

	for _, ns := range namespaces.Items {
		orig := ns.DeepCopy()

		if err := c.reconcileNamespacePodSecurityLabels(ctx, &ns, policy); err != nil {
			return err
		}

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

		if !reflect.DeepEqual(orig, &ns) {
			if err := c.Client.Update(ctx, &ns); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *VirtualClusterPolicyReconciler) reconcileNetworkPolicy(ctx context.Context, namespace string, policy *v1alpha1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling NetworkPolicy")

	var cidrList []string
	if c.ClusterCIDR != "" {
		cidrList = []string{c.ClusterCIDR}
	} else {
		var nodeList v1.NodeList
		if err := c.Client.List(ctx, &nodeList); err != nil {
			return err
		}

		for _, node := range nodeList.Items {
			cidrList = append(cidrList, node.Spec.PodCIDRs...)
		}
	}

	networkPolicy := networkPolicy(namespace, policy, cidrList)

	if err := ctrl.SetControllerReference(policy, networkPolicy, c.Scheme); err != nil {
		return err
	}

	// if disabled then delete the existing network policy
	if policy.Spec.DisableNetworkPolicy {
		err := c.Client.Delete(ctx, networkPolicy)
		return client.IgnoreNotFound(err)
	}

	// otherwise try to create/update
	err := c.Client.Create(ctx, networkPolicy)
	if apierrors.IsAlreadyExists(err) {
		return c.Client.Update(ctx, networkPolicy)
	}

	return err
}

func (c *VirtualClusterPolicyReconciler) reconcileNamespacePodSecurityLabels(ctx context.Context, namespace *v1.Namespace, policy *v1alpha1.VirtualClusterPolicy) error {
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

func (c *VirtualClusterPolicyReconciler) reconcileQuota(ctx context.Context, namespace string, policy *v1alpha1.VirtualClusterPolicy) error {
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
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespace,
			Labels: map[string]string{
				ManagedByLabelKey: VirtualPolicyControllerName,
			},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "ResourceQuota",
			APIVersion: "v1",
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
	log.Info("Reconciling VirtualClusterPolicy Limit")

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
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespace,
			Labels: map[string]string{
				ManagedByLabelKey: VirtualPolicyControllerName,
			},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "LimitRange",
			APIVersion: "v1",
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

	var err error

	for range clusters.Items {
		// TODO update status

		// oldClusterSpec := cluster.Spec

		// if cluster.Spec.PriorityClass != policy.Spec.DefaultPriorityClass {
		// 	cluster.Spec.PriorityClass = policy.Spec.DefaultPriorityClass
		// }

		// if !reflect.DeepEqual(cluster.Spec.NodeSelector, policy.Spec.DefaultNodeSelector) {
		// 	cluster.Spec.NodeSelector = policy.Spec.DefaultNodeSelector
		// }

		// if !reflect.DeepEqual(oldClusterSpec, cluster.Spec) {
		// 	// continue updating also the other clusters even if an error occurred
		// 	// TODO add status?
		// 	err = errors.Join(c.Client.Update(ctx, &cluster))
		// }
	}

	return err
}

func networkPolicy(namespaceName string, policy *v1alpha1.VirtualClusterPolicy, cidrList []string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespaceName,
			Labels: map[string]string{
				ManagedByLabelKey: VirtualPolicyControllerName,
			},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		Spec: networkingv1.NetworkPolicySpec{
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{},
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							IPBlock: &networkingv1.IPBlock{
								CIDR:   "0.0.0.0/0",
								Except: cidrList,
							},
						},
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": namespaceName,
								},
							},
						},
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": metav1.NamespaceSystem,
								},
							},
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"k8s-app": "kube-dns",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (c *VirtualClusterPolicyReconciler) cleanupNamespaces(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("deleting resources")

	fmt.Println("CLEANUPPPPP")

	requirement, err := labels.NewRequirement(VCPLabelKey, selection.DoesNotExist, nil)
	if err != nil {
		return err
	}

	sel := labels.NewSelector().Add(*requirement)

	var namespaces v1.NamespaceList
	if err := c.Client.List(ctx, &namespaces, client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return err
	}

	fmt.Println("found", len(namespaces.Items), "namespaces")

	for _, ns := range namespaces.Items {
		deleteOpts := []client.DeleteAllOfOption{
			client.InNamespace(ns.Name),
			client.MatchingLabels{ManagedByLabelKey: VirtualPolicyControllerName},
		}

		if err := c.Client.DeleteAllOf(ctx, &networkingv1.NetworkPolicy{}, deleteOpts...); err != nil {
			return err
		}

		if err := c.Client.DeleteAllOf(ctx, &v1.ResourceQuota{}, deleteOpts...); err != nil {
			return err
		}

		if err := c.Client.DeleteAllOf(ctx, &v1.LimitRange{}, deleteOpts...); err != nil {
			return err
		}
	}

	return nil
}
