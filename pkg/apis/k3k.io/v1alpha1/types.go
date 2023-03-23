package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Cluster struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	metav1.TypeMeta   `json:",inline"`

	Spec   ClusterSpec   `json:"spec"`
	Status ClusterStatus `json:"status"`
}

type ClusterSpec struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Servers     *int32 `json:"servers"`
	Agents      *int32 `json:"agents"`
	Token       string `json:"token"`
	ClusterCIDR string `json:"clusterCIDR,omitempty"`
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
	ClusterDNS  string `json:"clusterDNS,omitempty"`

	ServerArgs []string `json:"serverArgs,omitempty"`
	AgentArgs  []string `json:"agentArgs,omitempty"`

	Expose *ExposeConfig `json:"expose,omitempty"`
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

type ClusterStatus struct {
	OverrideClusterCIDR bool   `json:"overrideClusterCIDR"`
	OverrideServiceCIDR bool   `json:"overrideServiceCIDR"`
	ClusterCIDR         string `json:"clusterCIDR,omitempty"`
	ServiceCIDR         string `json:"serviceCIDR,omitempty"`
	ClusterDNS          string `json:"clusterDNS,omitempty"`
}

type Allocation struct {
	ClusterName string `json:"clusterName"`
	Issued      int64  `json:"issued"`
	IPNet       string `json:"ipNet"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type CIDRAllocationPool struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	metav1.TypeMeta   `json:",inline"`

	Spec   CIDRAllocationPoolSpec   `json:"spec"`
	Status CIDRAllocationPoolStatus `json:"status"`
}

type CIDRAllocationPoolSpec struct {
	DefaultClusterCIDR string `json:"defaultClusterCIDR"`
}

type CIDRAllocationPoolStatus struct {
	Pool []Allocation `json:"pool"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type CIDRAllocationPoolList struct {
	metav1.ListMeta `json:"metadata,omitempty"`
	metav1.TypeMeta `json:",inline"`

	Items []CIDRAllocationPool `json:"items"`
}
