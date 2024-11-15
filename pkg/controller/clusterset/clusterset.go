package clusterset

import (
	"context"
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/log"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	clusterSetController    = "k3k-clusterset-controller"
	allTrafficCIDR          = "0.0.0.0/0"
	maxConcurrentReconciles = 1
)

type ClusterSetReconciler struct {
	Client      ctrlruntimeclient.Client
	Scheme      *runtime.Scheme
	ClusterCIDR string
	logger      *log.Logger
}

// Add adds a new controller to the manager
func Add(ctx context.Context, mgr manager.Manager, clusterCIDR string, logger *log.Logger) error {
	// initialize a new Reconciler
	reconciler := ClusterSetReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterCIDR: clusterCIDR,
		logger:      logger.Named(clusterSetController),
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ClusterSet{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(&reconciler)
}

func (c *ClusterSetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := c.logger.With("ClusterSet", req.NamespacedName)

	var clusterSet v1alpha1.ClusterSet
	if err := c.Client.Get(ctx, req.NamespacedName, &clusterSet); err != nil {
		return reconcile.Result{}, err
	}

	// Check if a ClusterSet in the same namespace already exists.
	// We retry in case of timeout because it could happen the Informer needs to sync
	clusterSetList := &v1alpha1.ClusterSetList{}
	listOpts := &ctrlruntimeclient.ListOptions{Namespace: req.Namespace}
	err := retry.OnError(retry.DefaultBackoff, apierrors.IsTimeout, func() error {
		return c.Client.List(ctx, clusterSetList, listOpts)
	})
	if err != nil {
		return reconcile.Result{}, err
	}

	for _, cs := range clusterSetList.Items {
		if cs.Name != req.Name {
			//TODO: should we update the Status condition of the ClusterSet, instead of deleting it?
			if err := c.Client.Delete(context.Background(), &clusterSet); err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, fmt.Errorf("a ClusterSet in this namespace already exists: %s", cs.Name)
		}
	}

	if !clusterSet.Spec.DisableNetworkPolicy {
		log.Info("Creating NetworkPolicy")
		setNetworkPolicy, err := netpol(ctx, c.ClusterCIDR, &clusterSet, c.Client)
		if err != nil {
			return reconcile.Result{}, err
		}
		if err := c.Client.Create(ctx, setNetworkPolicy); err != nil {
			if apierrors.IsAlreadyExists(err) {
				if err := c.Client.Update(ctx, setNetworkPolicy); err != nil {
					return reconcile.Result{}, err
				}
			}
			return reconcile.Result{}, err
		}
	}
	// TODO: Add resource quota for clustersets
	// if clusterSet.Spec.MaxLimits != nil {
	// 	quota := v1.ResourceQuota{
	// 		ObjectMeta: metav1.ObjectMeta{
	// 			Name:      "clusterset-quota",
	// 			Namespace: clusterSet.Namespace,
	// 			OwnerReferences: []metav1.OwnerReference{
	// 				{
	// 					UID:        clusterSet.UID,
	// 					Name:       clusterSet.Name,
	// 					APIVersion: clusterSet.APIVersion,
	// 					Kind:       clusterSet.Kind,
	// 				},
	// 			},
	// 		},
	// 	}
	// 	quota.Spec.Hard = clusterSet.Spec.MaxLimits
	// 	if err := c.Client.Create(ctx, &quota); err != nil {
	// 		return reconcile.Result{}, fmt.Errorf("unable to create resource quota from cluster set: %w", err)
	// 	}
	// }
	return reconcile.Result{}, nil
}

func netpol(ctx context.Context, clusterCIDR string, clusterSet *v1alpha1.ClusterSet, client ctrlruntimeclient.Client) (*networkingv1.NetworkPolicy, error) {
	var cidrList []string
	if clusterCIDR == "" {
		var nodeList v1.NodeList
		if err := client.List(ctx, &nodeList); err != nil {
			return nil, err
		}
		for _, node := range nodeList.Items {
			cidrList = append(cidrList, node.Spec.PodCIDRs...)
		}
	} else {
		cidrList = []string{clusterCIDR}
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
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
