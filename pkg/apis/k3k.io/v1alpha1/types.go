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
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Servers     *int32   `json:"servers"`
	Agents      *int32   `json:"agents"`
	Token       string   `json:"token"`
	ClusterCIDR string   `json:"clusterCIDR,omitempty"`
	ServiceCIDR string   `json:"serviceCIDR,omitempty"`
	ClusterDNS  string   `json:"clusterDNS,omitempty"`
	ServerArgs  []string `json:"serverArgs,omitempty"`
	AgentArgs   []string `json:"agentArgs,omitempty"`
	TLSSANs     []string `json:"tlsSANs,omitempty"`

	Persistence *PersistenceConfig `json:"persistence,omitempty"`
	Expose      *ExposeConfig      `json:"expose,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ClusterList struct {
	metav1.ListMeta `json:"metadata,omitempty"`
	metav1.TypeMeta `json:",inline"`

	Items []Cluster `json:"items"`
}

type PersistenceConfig struct {
	// Type can be ephermal, static, dynamic
	Type               string `json:"type"`
	StorageClassName   string `json:"storageClassName,omitempty"`
	StorageRequestSize string `json:"storageRequestSize,omitempty"`
}

type ExposeConfig struct {
	Ingress      *IngressConfig      `json:"ingress"`
	LoadBalancer *LoadBalancerConfig `json:"loadbalancer"`
	NodePort     *NodePortConfig     `json:"nodePort"`
}

type IngressConfig struct {
	Enabled          bool   `json:"enabled"`
	IngressClassName string `json:"ingressClassName"`
}

type LoadBalancerConfig struct {
	Enabled bool `json:"enabled"`
}

type NodePortConfig struct {
	Enabled bool `json:"enabled"`
}

type ClusterStatus struct {
	ClusterCIDR string `json:"clusterCIDR,omitempty"`
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
	ClusterDNS  string `json:"clusterDNS,omitempty"`
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
