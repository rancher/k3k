package v1beta1

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k3k "github.com/rancher/k3k/pkg/apis/k3k.io"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: k3k.GroupName, Version: "v1beta1"}
	SchemBuilder       = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemBuilder.AddToScheme
)

func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(SchemeGroupVersion,
		&Cluster{},
		&ClusterList{},
		&VirtualClusterPolicy{},
		&VirtualClusterPolicyList{},
	)
	metav1.AddToGroupVersion(s, SchemeGroupVersion)

	return nil
}
