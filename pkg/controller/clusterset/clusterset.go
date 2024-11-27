package clusterset

import (
	"context"
	"reflect"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/log"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		Owns(&networkingv1.NetworkPolicy{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(&reconciler)
}

func (c *ClusterSetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := c.logger.With("ClusterSet", req.NamespacedName)

	var clusterSet v1alpha1.ClusterSet
	if err := c.Client.Get(ctx, req.NamespacedName, &clusterSet); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if err := c.reconcileNetworkPolicy(ctx, log, &clusterSet); err != nil {
		return reconcile.Result{}, err
	}

	if err := c.reconcileNamespacePodSecurityLabels(ctx, log, &clusterSet); err != nil {
		return reconcile.Result{}, err
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

func (c *ClusterSetReconciler) reconcileNetworkPolicy(ctx context.Context, log *zap.SugaredLogger, clusterSet *v1alpha1.ClusterSet) error {
	log.Info("reconciling NetworkPolicy")

	networkPolicy, err := netpol(ctx, c.ClusterCIDR, clusterSet, c.Client)
	if err != nil {
		return err
	}

	if err = ctrl.SetControllerReference(clusterSet, networkPolicy, c.Scheme); err != nil {
		return err
	}

	// if disabled then delete the existing network policy
	if clusterSet.Spec.DisableNetworkPolicy {
		err := c.Client.Delete(ctx, networkPolicy)
		return client.IgnoreNotFound(err)
	}

	// otherwise try to create/update
	err = c.Client.Create(ctx, networkPolicy)
	if apierrors.IsAlreadyExists(err) {
		return c.Client.Update(ctx, networkPolicy)
	}

	return err
}

func netpol(ctx context.Context, clusterCIDR string, clusterSet *v1alpha1.ClusterSet, client ctrlruntimeclient.Client) (*networkingv1.NetworkPolicy, error) {
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

func (c *ClusterSetReconciler) reconcileNamespacePodSecurityLabels(ctx context.Context, log *zap.SugaredLogger, clusterSet *v1alpha1.ClusterSet) error {
	log.Info("reconciling Namespace")

	var ns v1.Namespace
	key := types.NamespacedName{Name: clusterSet.Namespace}
	if err := c.Client.Get(ctx, key, &ns); err != nil {
		return err
	}

	newLabels := map[string]string{}
	for k, v := range ns.Labels {
		newLabels[k] = v
	}

	// cleanup of old labels
	delete(newLabels, "pod-security.kubernetes.io/enforce")
	delete(newLabels, "pod-security.kubernetes.io/enforce-version")
	delete(newLabels, "pod-security.kubernetes.io/warn")
	delete(newLabels, "pod-security.kubernetes.io/warn-version")

	// if a PSA level is specified add the proper labels
	if clusterSet.Spec.PodSecurityAdmissionLevel != nil {
		psaLevel := *clusterSet.Spec.PodSecurityAdmissionLevel

		newLabels["pod-security.kubernetes.io/enforce"] = string(psaLevel)
		newLabels["pod-security.kubernetes.io/enforce-version"] = "latest"

		// skip the 'warn' only for the privileged PSA level
		if psaLevel != v1alpha1.PrivilegedPodSecurityAdmissionLevel {
			newLabels["pod-security.kubernetes.io/warn"] = string(psaLevel)
			newLabels["pod-security.kubernetes.io/warn-version"] = "latest"
		}
	}

	if !reflect.DeepEqual(ns.Labels, newLabels) {
		log.Debug("labels changed, updating namespace")

		ns.Labels = newLabels
		return c.Client.Update(ctx, &ns)
	}
	return nil
}
