package policy

import (
	"context"
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
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	clusterPolicyController = "k3k-clusterpolicy-controller"
	allTrafficCIDR          = "0.0.0.0/0"
)

type VirtualClusterPolicyReconciler struct {
	Client      client.Client
	Scheme      *runtime.Scheme
	ClusterCIDR string
}

// Add adds a new controller to the manager
func Add(ctx context.Context, mgr manager.Manager, clusterCIDR string) error {
	// initialize a new Reconciler
	reconciler := VirtualClusterPolicyReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterCIDR: clusterCIDR,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VirtualClusterPolicy{}).
		Complete(&reconciler)
}

// namespaceLabelsPredicate returns a predicate that will allow a reconciliation if the labels of a Namespace changed
// func namespaceLabelsPredicate() predicate.Predicate {
// 	return predicate.Funcs{
// 		UpdateFunc: func(e event.UpdateEvent) bool {
// 			oldObj := e.ObjectOld.(*v1.Namespace)
// 			newObj := e.ObjectNew.(*v1.Namespace)

// 			return !reflect.DeepEqual(oldObj.Labels, newObj.Labels)
// 		},
// 	}
// }

func (c *VirtualClusterPolicyReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("clusterpolicy", req.NamespacedName)
	ctx = ctrl.LoggerInto(ctx, log) // enrich the current logger

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
		// "app.kubernetes.io/managed-by": "k3k-policy-controller",
		NamespacePolicyLabel: policy.Name,
	}

	var namespaces v1.NamespaceList
	if err := c.Client.List(ctx, &namespaces, listOpts); err != nil {
		return err
	}

	for _, ns := range namespaces.Items {
		orig := ns.DeepCopy()

		// if err := reconcileNamespace(ctx, c.Client, c.Scheme, &ns, policy, c.ClusterCIDR); err != nil {
		// 	return err
		// }

		if !reflect.DeepEqual(orig, &ns) {
			if err := c.Client.Update(ctx, &ns); err != nil {
				return err
			}
		}
	}

	// if err := c.reconcileNetworkPolicy(ctx, policy); err != nil {
	// 	return err
	// }

	// if err := c.reconcileNamespacePodSecurityLabels(ctx, policy); err != nil {
	// 	return err
	// }

	if err := c.reconcileLimit(ctx, policy); err != nil {
		return err
	}

	// if err := c.reconcileQuota(ctx, policy); err != nil {
	// 	return err
	// }

	// if err := c.reconcileClusters(ctx, policy); err != nil {
	// 	return err
	// }

	return nil
}

// func (c *VirtualClusterPolicyReconciler) reconcileNetworkPolicy(ctx context.Context, policy *v1alpha1.VirtualClusterPolicy) error {
// 	log := ctrl.LoggerFrom(ctx)
// 	log.Info("reconciling NetworkPolicy")

// 	networkPolicy, err := netpol(ctx, c.Client, "", policy, c.ClusterCIDR)
// 	if err != nil {
// 		return err
// 	}

// 	if err = ctrl.SetControllerReference(policy, networkPolicy, c.Scheme); err != nil {
// 		return err
// 	}

// 	// if disabled then delete the existing network policy
// 	if policy.Spec.DisableNetworkPolicy {
// 		err := c.Client.Delete(ctx, networkPolicy)
// 		return client.IgnoreNotFound(err)
// 	}

// 	// otherwise try to create/update
// 	err = c.Client.Create(ctx, networkPolicy)
// 	if apierrors.IsAlreadyExists(err) {
// 		return c.Client.Update(ctx, networkPolicy)
// 	}

// 	return err
// }

func netpol(ctx context.Context, client client.Client, namespaceName string, policy *v1alpha1.VirtualClusterPolicy, clusterCIDR string) (*networkingv1.NetworkPolicy, error) {
	var cidrList []string

	if clusterCIDR != "" {
		cidrList = []string{clusterCIDR}
	} else {
		var nodeList v1.NodeList
		if err := client.List(ctx, &nodeList); err != nil {
			return nil, err
		}

		for _, node := range nodeList.Items {
			cidrList = append(cidrList, node.Spec.PodCIDRs...)
		}
	}

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
								CIDR:   allTrafficCIDR,
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
	}, nil
}

func (c *VirtualClusterPolicyReconciler) reconcileLimit(ctx context.Context, policy *v1alpha1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling VirtualClusterPolicy Limit")

	// delete limitrange if spec.limits isnt specified.
	if policy.Spec.Limit == nil {
		var toDeleteLimitRange v1.LimitRange

		key := types.NamespacedName{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: policy.Namespace,
		}

		if err := c.Client.Get(ctx, key, &toDeleteLimitRange); err != nil {
			return client.IgnoreNotFound(err)
		}

		return c.Client.Delete(ctx, &toDeleteLimitRange)
	}

	limitRange := limitRange(policy)
	if err := ctrl.SetControllerReference(policy, &limitRange, c.Scheme); err != nil {
		return err
	}

	if err := c.Client.Create(ctx, &limitRange); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return c.Client.Update(ctx, &limitRange)
		}
	}

	return nil
}

func limitRange(policy *v1alpha1.VirtualClusterPolicy) v1.LimitRange {
	return v1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: policy.Namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "LimitRange",
			APIVersion: "v1",
		},
		Spec: *policy.Spec.Limit,
	}
}
