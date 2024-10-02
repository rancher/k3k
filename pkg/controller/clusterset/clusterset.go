package clusterset

import (
	"context"
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	clusterSetController    = "k3k-clusterset-controller"
	networkPolicyName       = "k3k-cluster-netpol"
	allTrafficCIDR          = "0.0.0.0/0"
	maxConcurrentReconciles = 1
)

type ClusterSetReconciler struct {
	Client      ctrlruntimeclient.Client
	Scheme      *runtime.Scheme
	ClusterCIDR string
}

// Add adds a new controller to the manager
func Add(ctx context.Context, mgr manager.Manager, clusterCIDR string) error {
	// initialize a new Reconciler
	reconciler := ClusterSetReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterCIDR: clusterCIDR,
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ClusterSet{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(&reconciler)
}

func (c *ClusterSetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	klog.Infof("%#v", req)
	var clusterSet v1alpha1.ClusterSet
	if err := c.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, &clusterSet); err != nil {
		return reconcile.Result{}, fmt.Errorf("unable to get the clusterset: %w", err)
	}

	klog.Infof("got a clusterset: %v", clusterSet)
	// create network policy
	setNetworkPolicy, err := c.netpol(ctx, &clusterSet)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("unable to make a networkpolicy for cluster set: %w", err)
	}
	if err := c.Client.Create(ctx, setNetworkPolicy); err != nil {
		return reconcile.Result{}, fmt.Errorf("unable to create networkpolicy for clusterset: %w", err)
	}
	if clusterSet.Spec.MaxLimits != nil {
		quota := v1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "clusterset-quota",
				Namespace: clusterSet.Namespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						UID:        clusterSet.UID,
						Name:       clusterSet.Name,
						APIVersion: clusterSet.APIVersion,
						Kind:       clusterSet.Kind,
					},
				},
			},
		}
		quota.Spec.Hard = clusterSet.Spec.MaxLimits
		if err := c.Client.Create(ctx, &quota); err != nil {
			return reconcile.Result{}, fmt.Errorf("unable to create resource quota from cluster set: %w", err)
		}
	}
	return reconcile.Result{}, nil
}

func (c *ClusterSetReconciler) netpol(ctx context.Context, clusterSet *v1alpha1.ClusterSet) (*networkingv1.NetworkPolicy, error) {
	var cidrList []string
	if c.ClusterCIDR == "" {
		var nodeList v1.NodeList
		if err := c.Client.List(ctx, &nodeList); err != nil {
			return nil, err
		}
		for _, node := range nodeList.Items {
			cidrList = append(cidrList, node.Spec.PodCIDRs...)
		}
	} else {
		cidrList = []string{c.ClusterCIDR}
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkPolicyName,
			Namespace: clusterSet.Namespace,
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
									"kubernetes.io/metadata.name": clusterSet.Namespace,
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
