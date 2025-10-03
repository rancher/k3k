package cluster

import (
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
)

func newClusterPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		owner := metav1.GetControllerOf(object)

		return owner != nil &&
			owner.Kind == "Cluster" &&
			owner.APIVersion == v1alpha1.SchemeGroupVersion.String()
	})
}

func clusterNamespacedName(object client.Object) types.NamespacedName {
	var clusterName string

	owner := metav1.GetControllerOf(object)
	if owner != nil && owner.Kind == "Cluster" && owner.APIVersion == v1alpha1.SchemeGroupVersion.String() {
		clusterName = owner.Name
	} else {
		clusterName = object.GetLabels()[translate.ClusterNameLabel]
	}

	return types.NamespacedName{
		Name:      clusterName,
		Namespace: object.GetNamespace(),
	}
}
