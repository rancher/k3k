package cluster

import (
	"context"
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type webhookHandler struct {
	client ctrlruntimeclient.Client
	scheme *runtime.Scheme
}

func AddWebhookHandler(ctx context.Context, mgr manager.Manager) error {
	handler := webhookHandler{
		client: mgr.GetClient(),
		scheme: mgr.GetScheme(),
	}
	return ctrl.NewWebhookManagedBy(mgr).For(&v1alpha1.Cluster{}).WithDefaulter(&handler).WithValidator(&handler).Complete()
}

func (w *webhookHandler) Default(ctx context.Context, obj runtime.Object) error {
	cluster, ok := obj.(*v1alpha1.Cluster)
	if !ok {
		return fmt.Errorf("invalid request: object was type %t not cluster", obj)
	}
	klog.Info("recieved mutate request for %+v", cluster)
	clusterSet, found, err := w.findClusterSet(ctx, cluster)
	if err != nil {
		return fmt.Errorf("error when finding cluster set: %w", err)
	}
	if !found {
		// this cluster isn't in a cluster set, don't apply any defaults
		return nil
	}
	clusterSetLimits := clusterSet.Spec.DefaultLimits
	clusterLimits := cluster.Spec.Limit
	if clusterSetLimits != nil {
		if clusterLimits == nil {
			clusterLimits = clusterSetLimits
		}
		defaultLimits(clusterSetLimits.ServerLimit, clusterLimits.ServerLimit)
		defaultLimits(clusterSetLimits.WorkerLimit, clusterLimits.WorkerLimit)
		cluster.Spec.Limit = clusterLimits
	}
	// values are overriden for node selector, which applies to all clusters in the set
	for key, value := range clusterSet.Spec.DefaultNodeSelector {
		cluster.Spec.NodeSelector[key] = value
	}
	return nil
}

// defaultLimits adds missing keys from default into current. Existing values are not replaced
func defaultLimits(defaults v1.ResourceList, current v1.ResourceList) {
	for name, limit := range defaults {
		if _, ok := current[name]; !ok {
			current[name] = limit
		}
	}
}

func (w *webhookHandler) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	cluster, ok := obj.(*v1alpha1.Cluster)
	if !ok {
		return fmt.Errorf("invalid request: object was type %t not cluster", obj)
	}
	clusterSet, found, err := w.findClusterSet(ctx, cluster)
	if err != nil {
		return fmt.Errorf("error when finding cluster set: %w", err)
	}
	if !found {
		// this cluster isn't in a cluster set, don't do any validation
		return nil
	}
	return w.validateLimits(ctx, cluster, clusterSet)
}

func (w *webhookHandler) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	// note that since we only check new cluster, if the oldCluster was made invalid by changing the clusterSet
	// the next operation on the cluster must correct the error, or no request can go through
	cluster, ok := newObj.(*v1alpha1.Cluster)
	if !ok {
		return fmt.Errorf("invalid request: object was type %t not cluster", newObj)
	}
	clusterSet, found, err := w.findClusterSet(ctx, cluster)
	if err != nil {
		return fmt.Errorf("error when finding cluster set: %w", err)
	}
	if !found {
		// this cluster isn't in a cluster set, don't do any validation
		return nil
	}
	return w.validateLimits(ctx, cluster, clusterSet)
}

func (w *webhookHandler) ValidateDelete(ctx context.Context, obj runtime.Object) error {
	// Deleting a cluster is always valid
	return nil
}

func (w *webhookHandler) validateLimits(ctx context.Context, cluster *v1alpha1.Cluster, clusterSet *v1alpha1.ClusterSet) error {
	maxLimits := clusterSet.Spec.MaxLimits
	if maxLimits == nil {
		// nothing to validate if there are no limits
		return nil
	}
	if cluster.Spec.Limit == nil || cluster.Spec.Limit.ServerLimit == nil || cluster.Spec.Limit.WorkerLimit == nil {
		// this should never happen because of the defaulter, but is done to guard a nil/panic below
		return fmt.Errorf("cluster %s has no limits set but is in a cluster set with limits", cluster.Name)
	}
	serverLimit := cluster.Spec.Limit.ServerLimit
	workerLimit := cluster.Spec.Limit.WorkerLimit
	for limit := range maxLimits {
		if _, ok := serverLimit[limit]; !ok {
			return fmt.Errorf("a limit was set for resource %s but no cluster limit was provided", limit)
		}
		if _, ok := workerLimit[limit]; !ok {
			return fmt.Errorf("a limit was set for resource %s but no cluster limit was provided", limit)
		}
	}
	usedLimits, err := w.findUsedLimits(ctx, clusterSet, cluster)
	if err != nil {
		return fmt.Errorf("unable to find current used limits: %w", err)
	}
	klog.Infof("used: %+v, max: %+v", usedLimits, maxLimits)
	if exceedsLimits(maxLimits, usedLimits) {
		return fmt.Errorf("new cluster would exceed limits")
	}
	return nil
}

func (w *webhookHandler) findClusterSet(ctx context.Context, cluster *v1alpha1.Cluster) (*v1alpha1.ClusterSet, bool, error) {
	var clusterSets v1alpha1.ClusterSetList
	err := w.client.List(ctx, &clusterSets, ctrlruntimeclient.InNamespace(cluster.Namespace))
	if err != nil {
		return nil, false, fmt.Errorf("unable to list cluster sets: %w", err)
	}
	switch len(clusterSets.Items) {
	case 0:
		return nil, false, nil
	case 1:
		return &clusterSets.Items[0], true, nil
	default:
		return nil, false, fmt.Errorf("expected only one clusterset, found %d", len(clusterSets.Items))

	}
}

func (w *webhookHandler) findUsedLimits(ctx context.Context, clusterSet *v1alpha1.ClusterSet, newCluster *v1alpha1.Cluster) (v1.ResourceList, error) {
	var clusterList v1alpha1.ClusterList
	if err := w.client.List(ctx, &clusterList, ctrlruntimeclient.InNamespace(clusterSet.Namespace)); err != nil {
		return nil, fmt.Errorf("unable to list clusters: %w", err)
	}
	usedLimits := v1.ResourceList{}
	for _, cluster := range clusterList.Items {
		// skip new cluster - if on update we need to add the new proposed values, not the existing values
		if cluster.Spec.Limit == nil || cluster.Name == newCluster.Name {
			// note that this will cause clusters with no values set to be beyond used calculations
			continue
		}
		addClusterLimits(usedLimits, &cluster)
	}
	addClusterLimits(usedLimits, newCluster)
	return usedLimits, nil
}

func addClusterLimits(usedLimits v1.ResourceList, cluster *v1alpha1.Cluster) {
	for i := 0; i < int(*cluster.Spec.Agents); i++ {
		usedLimits = addLimits(usedLimits, cluster.Spec.Limit.WorkerLimit)
	}
	for i := 0; i < int(*cluster.Spec.Servers); i++ {
		usedLimits = addLimits(usedLimits, cluster.Spec.Limit.ServerLimit)
	}
}

func addLimits(currentList v1.ResourceList, newList v1.ResourceList) v1.ResourceList {
	for key, value := range newList {
		current, ok := currentList[key]
		if !ok {
			// deep copy to avoid mutating the next time that we add something on
			currentList[key] = value.DeepCopy()
			continue
		}
		current.Add(value)
		currentList[key] = current
	}
	return currentList
}

func exceedsLimits(maxLimit v1.ResourceList, usedLimit v1.ResourceList) bool {
	for key, value := range maxLimit {
		used, ok := usedLimit[key]
		if !ok {
			// not present means there is no usage currently
			continue
		}
		// used > max, so this would exceed the max
		if used.Cmp(value) == 1 {
			return true
		}
	}
	return false
}
