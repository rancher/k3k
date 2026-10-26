package policy

import (
	"context"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
)

func (c *VirtualClusterPolicyReconciler) reconcileNetworkPolicy(ctx context.Context, namespace string, policy *v1beta1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Reconciling NetworkPolicy")

	cidrList, err := FindPodCIDRs(ctx, c.Client, c.ClusterCIDR)
	if err != nil {
		return err
	}

	networkPolicy := networkPolicy(namespace, policy, cidrList)

	if err := ctrl.SetControllerReference(policy, networkPolicy, c.Scheme); err != nil {
		return err
	}

	// if disabled then delete the existing network policy
	if policy.Spec.DisableNetworkPolicy {
		log.V(1).Info("Deleting NetworkPolicy")

		return client.IgnoreNotFound(c.Client.Delete(ctx, networkPolicy))
	}

	log.V(1).Info("Creating NetworkPolicy")

	// otherwise try to create/update
	err = c.Client.Create(ctx, networkPolicy)
	if apierrors.IsAlreadyExists(err) {
		log.V(1).Info("NetworkPolicy already exists, updating.")

		return c.Client.Update(ctx, networkPolicy)
	}

	return err
}

// FindPodCIDRs returns the CIDR ranges to exclude from the isolation NetworkPolicy's egress allow-list,
// so that pod-to-pod traffic on the host's real pod network is blocked.
// If clusterCIDR is set (the --cluster-cidr controller flag) it's used as-is;
// otherwise it's inferred from the host Nodes' Spec.PodCIDR(s).
// Returns an empty list, and logs a warning, if it can't be determined either way.
func FindPodCIDRs(ctx context.Context, cl client.Client, clusterCIDR string) ([]string, error) {
	log := ctrl.LoggerFrom(ctx)

	if clusterCIDR != "" {
		return []string{clusterCIDR}, nil
	}

	var nodeList corev1.NodeList
	if err := cl.List(ctx, &nodeList); err != nil {
		return nil, err
	}

	cidrs := sets.New[string]()

	for _, node := range nodeList.Items {
		cidrs.Insert(node.Spec.PodCIDRs...)

		if node.Spec.PodCIDR != "" {
			cidrs.Insert(node.Spec.PodCIDR)
		}
	}

	cidrList := sets.List(cidrs)

	// node.Spec.PodCIDR is only populated when kube-controller-manager runs with --allocate-node-cidrs.
	// K3s and RKE2 enable it by default (cluster-cidr 10.42.0.0/16),
	// so the field is populated there regardless of CNI.
	// Some setups don't set it, i.e. Cilium with cluster-pool IPAM (Cilium's default),
	// which manages per-node CIDRs in the v2.CiliumNode CRD and doesn't need Kubernetes to hand out PodCIDRs.
	// See https://docs.cilium.io/en/stable/network/concepts/ipam/cluster-pool/
	// Without it the egress rule can't exclude the pod network, so cross-cluster pod
	// isolation would silently not be enforced. Set --cluster-cidr in that case.
	if len(cidrList) == 0 {
		log.Info("Could not determine the pod CIDR from the nodes; cross-cluster pod isolation will not be enforced. " +
			"Set the --cluster-cidr flag on the k3k controller to fix this.")
	}

	return cidrList, nil
}

func networkPolicy(namespaceName string, policy *v1beta1.VirtualClusterPolicy, cidrList []string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespaceName,
			Labels: map[string]string{
				ManagedByLabelKey:  VirtualPolicyControllerName,
				PolicyNameLabelKey: policy.Name,
			},
		},
		Spec: IsolationNetworkPolicySpec(namespaceName, cidrList),
	}
}

// IsolationNetworkPolicySpec returns the spec of the default NetworkPolicy that isolates the synced
// workload pods of the virtual clusters in a namespace. It's shared by standalone clusters and
// namespaces bound to a VirtualClusterPolicy.
//
// Egress is allowed everywhere except the host pod network (podCIDRs), plus the namespace itself
// and the host kube-dns.
func IsolationNetworkPolicySpec(namespaceName string, podCIDRs []string) networkingv1.NetworkPolicySpec {
	return networkingv1.NetworkPolicySpec{
		// Isolate synced workload pods
		PodSelector: metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      translate.ClusterNameLabel,
					Operator: metav1.LabelSelectorOpExists,
				},
			},
		},
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
							Except: podCIDRs,
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
	}
}
