package v1alpha1

import (
	k3k "github.com/galal-hussein/k3k/pkg/apis/k3k.io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var SchemeGroupVersion = schema.GroupVersion{Group: k3k.GroupName, Version: "v1alpha1"}

var (
	SchemBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme  = SchemBuilder.AddToScheme
)

func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(SchemeGroupVersion,
		&Cluster{},
		&ClusterList{},
		&CIDRAllocationPool{},
		&CIDRAllocationPoolList{})
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
	return nil
}
