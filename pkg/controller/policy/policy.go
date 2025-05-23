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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type VirtualClusterPolicyReconciler struct {
	Client      client.Client
	Scheme      *runtime.Scheme
	ClusterCIDR string
}

// AddControllers adds the necessary controllers to manage the Virtual Cluster policies
func AddControllers(mgr manager.Manager, clusterCIDR string) error {
	if err := addVirtualPolicyController(mgr); err != nil {
		return err
	}

	if err := addNamespaceController(mgr, clusterCIDR); err != nil {
		return err
	}

	return nil
}

// Add adds a new controller to the manager
func addVirtualPolicyController(mgr manager.Manager) error {
	reconciler := VirtualClusterPolicyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VirtualClusterPolicy{}).
		Watches(
			&v1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(mapVCPToNamespaces(reconciler)),
			builder.WithPredicates(updatePodCIDRNodePredicate),
		).
		Watches(
			&v1.Node{},
			handler.EnqueueRequestsFromMapFunc(mapVCPToNamespaces(reconciler)),
			builder.WithPredicates(updatePodCIDRNodePredicate),
		).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&v1.ResourceQuota{}).
		Owns(&v1alpha1.Cluster{}).
		Complete(&reconciler)
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

	for _, cluster := range clusters.Items {
		oldClusterSpec := cluster.Spec

		if cluster.Spec.PriorityClass != policy.Spec.DefaultPriorityClass {
			cluster.Spec.PriorityClass = policy.Spec.DefaultPriorityClass
		}

		if !reflect.DeepEqual(cluster.Spec.NodeSelector, policy.Spec.DefaultNodeSelector) {
			cluster.Spec.NodeSelector = policy.Spec.DefaultNodeSelector
		}

		if err := ctrl.SetControllerReference(policy, &cluster, c.Scheme); err != nil {
			return err
		}

		if !reflect.DeepEqual(oldClusterSpec, cluster.Spec) {
			// continue updating also the other clusters even if an error occurred
			// TODO add status?
			err = errors.Join(c.Client.Update(ctx, &cluster))
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

func networkPolicy(namespaceName string, policy *v1alpha1.VirtualClusterPolicy, cidrList []string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespaceName,
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
