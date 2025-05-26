package policy

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
			if len(node.Spec.PodCIDRs) > 0 {
				cidrList = append(cidrList, node.Spec.PodCIDRs...)
			} else {
				cidrList = append(cidrList, node.Spec.PodCIDR)
			}
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

func networkPolicy(namespaceName string, policy *v1alpha1.VirtualClusterPolicy, cidrList []string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
			Namespace: namespaceName,
			Labels: map[string]string{
				ManagedByLabelKey: VirtualPolicyControllerName,
			},
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
