package clusterset

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
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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
		Owns(&networkingv1.NetworkPolicy{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Watches(
			&v1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(namespaceEventHandler(reconciler)),
			builder.WithPredicates(namespaceLabelsPredicate()),
		).
		Watches(
			&v1alpha1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(sameNamespaceEventHandler(reconciler)),
		).
		Complete(&reconciler)
}

// namespaceEventHandler will enqueue reconciling requests for all the ClusterSets in the changed namespace
func namespaceEventHandler(reconciler ClusterSetReconciler) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		var (
			requests []reconcile.Request
			set      v1alpha1.ClusterSetList
		)

		_ = reconciler.Client.List(ctx, &set, client.InNamespace(obj.GetName()))

		for _, clusterSet := range set.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      clusterSet.Name,
					Namespace: obj.GetName(),
				},
			})
		}

		return requests
	}
}

// sameNamespaceEventHandler will enqueue reconciling requests for all the ClusterSets in the changed namespace
func sameNamespaceEventHandler(reconciler ClusterSetReconciler) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		var (
			requests []reconcile.Request
			set      v1alpha1.ClusterSetList
		)

		_ = reconciler.Client.List(ctx, &set, client.InNamespace(obj.GetNamespace()))

		for _, clusterSet := range set.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      clusterSet.Name,
					Namespace: obj.GetNamespace(),
				},
			})
		}

		return requests
	}
}

// namespaceLabelsPredicate returns a predicate that will allow a reconciliation if the labels of a Namespace changed
func namespaceLabelsPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObj := e.ObjectOld.(*v1.Namespace)
			newObj := e.ObjectNew.(*v1.Namespace)

			return !reflect.DeepEqual(oldObj.Labels, newObj.Labels)
		},
	}
}

func (c *ClusterSetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("clusterset", req.NamespacedName)
	ctx = ctrl.LoggerInto(ctx, log) // enrich the current logger

	var clusterSet v1alpha1.ClusterSet
	if err := c.Client.Get(ctx, req.NamespacedName, &clusterSet); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if err := c.reconcileNetworkPolicy(ctx, &clusterSet); err != nil {
		return reconcile.Result{}, err
	}

	if err := c.reconcileNamespacePodSecurityLabels(ctx, &clusterSet); err != nil {
		return reconcile.Result{}, err
	}

	if err := c.reconcileClusters(ctx, &clusterSet); err != nil {
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

func (c *ClusterSetReconciler) reconcileNetworkPolicy(ctx context.Context, clusterSet *v1alpha1.ClusterSet) error {
	log := ctrl.LoggerFrom(ctx)
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

func netpol(ctx context.Context, clusterCIDR string, clusterSet *v1alpha1.ClusterSet, client client.Client) (*networkingv1.NetworkPolicy, error) {
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

func (c *ClusterSetReconciler) reconcileNamespacePodSecurityLabels(ctx context.Context, clusterSet *v1alpha1.ClusterSet) error {
	log := ctrl.LoggerFrom(ctx)
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
		log.V(1).Info("labels changed, updating namespace")

		ns.Labels = newLabels

		return c.Client.Update(ctx, &ns)
	}

	return nil
}

func (c *ClusterSetReconciler) reconcileClusters(ctx context.Context, clusterSet *v1alpha1.ClusterSet) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling Clusters")

	var clusters v1alpha1.ClusterList
	if err := c.Client.List(ctx, &clusters, ctrlruntimeclient.InNamespace(clusterSet.Namespace)); err != nil {
		return err
	}

	var err error

	for _, cluster := range clusters.Items {
		oldClusterSpec := cluster.Spec

		if cluster.Spec.PriorityClass != clusterSet.Spec.DefaultPriorityClass {
			cluster.Spec.PriorityClass = clusterSet.Spec.DefaultPriorityClass
		}

		if !reflect.DeepEqual(cluster.Spec.NodeSelector, clusterSet.Spec.DefaultNodeSelector) {
			cluster.Spec.NodeSelector = clusterSet.Spec.DefaultNodeSelector
		}

		if !reflect.DeepEqual(oldClusterSpec, cluster.Spec) {
			// continue updating also the other clusters even if an error occurred
			err = errors.Join(c.Client.Update(ctx, &cluster))
		}
	}

	return err
}
