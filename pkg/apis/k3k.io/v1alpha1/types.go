package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Cluster struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	metav1.TypeMeta   `json:",inline"`

	Spec ClusterSpec `json:"spec"`
}

type ClusterSpec struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Servers *int32 `json:"servers"`
	Agents  *int32 `json:"agents"`
	Token   string `json:"token"`

	Expose ExposeConfig `json:"expose"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ClusterList struct {
	metav1.ListMeta `json:"metadata,omitempty"`
	metav1.TypeMeta `json:",inline"`

	Items []Cluster `json:"items"`
}

type ExposeConfig struct {
	Ingress      *IngressConfig      `json:"ingress"`
	LoadBalancer *LoadBalancerConfig `json:"loadbalancer"`
}

type IngressConfig struct {
	Enabled          bool   `json:"enabled"`
	IngressClassName string `json:"ingressClassName"`
}

type LoadBalancerConfig struct {
	Enabled bool `json:"enabled"`
}
