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
	Client      client.Client
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

	if err := c.reconcileLimit(ctx, &clusterSet); err != nil {
		return reconcile.Result{}, err
	}

	if err := c.reconcileQuota(ctx, &clusterSet); err != nil {
		return reconcile.Result{}, err
	}

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
	if err := c.Client.List(ctx, &clusters, client.InNamespace(clusterSet.Namespace)); err != nil {
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

func (c *ClusterSetReconciler) reconcileQuota(ctx context.Context, clusterSet *v1alpha1.ClusterSet) error {
	if clusterSet.Spec.Quota == nil {
		// check if resourceQuota object exists and deletes it.
		var toDeleteResourceQuota v1.ResourceQuota

		key := types.NamespacedName{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
			Namespace: clusterSet.Namespace,
		}

		if err := c.Client.Get(ctx, key, &toDeleteResourceQuota); err != nil {
			return client.IgnoreNotFound(err)
		}

		return c.Client.Delete(ctx, &toDeleteResourceQuota)
	}

	// create/update resource Quota
	resourceQuota := resourceQuota(clusterSet)

	if err := ctrl.SetControllerReference(clusterSet, &resourceQuota, c.Scheme); err != nil {
		return err
	}

	if err := c.Client.Create(ctx, &resourceQuota); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return c.Client.Update(ctx, &resourceQuota)
		}
	}

	return nil
}

func resourceQuota(clusterSet *v1alpha1.ClusterSet) v1.ResourceQuota {
	return v1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
			Namespace: clusterSet.Namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "ResourceQuota",
			APIVersion: "v1",
		},
		Spec: *clusterSet.Spec.Quota,
	}
}

func (c *ClusterSetReconciler) reconcileLimit(ctx context.Context, clusterSet *v1alpha1.ClusterSet) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling ClusterSet Limit")

	// delete limitrange if spec.limits isnt specified.
	if clusterSet.Spec.Limit == nil {
		var toDeleteLimitRange v1.LimitRange

		key := types.NamespacedName{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
			Namespace: clusterSet.Namespace,
		}

		if err := c.Client.Get(ctx, key, &toDeleteLimitRange); err != nil {
			return client.IgnoreNotFound(err)
		}

		if err := c.Client.Delete(ctx, &toDeleteLimitRange); err != nil {
			return err
		}
	}

	limitRange := limitRange(clusterSet)
	if err := ctrl.SetControllerReference(clusterSet, &limitRange, c.Scheme); err != nil {
		return err
	}

	if err := c.Client.Create(ctx, &limitRange); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return c.Client.Update(ctx, &limitRange)
		}
	}

	return nil
}

func limitRange(clusterSet *v1alpha1.ClusterSet) v1.LimitRange {
	return v1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
			Namespace: clusterSet.Namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "LimitRange",
			APIVersion: "v1",
		},
		Spec: *clusterSet.Spec.Limit,
	}
}
