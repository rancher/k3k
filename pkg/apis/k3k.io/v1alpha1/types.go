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
	// Version is a string representing the Kubernetes version to be used by the virtual nodes.
	Version string `json:"version"`
	// Servers is the number of K3s pods to run in server (controlplane) mode.
	Servers *int32 `json:"servers"`
	// Agents is the number of K3s pods to run in agent (worker) mode.
	Agents *int32 `json:"agents"`
	// Token is the token used to join the worker nodes to the cluster.
	Token string `json:"token"`
	// ClusterCIDR is the CIDR range for the pods of the cluster. Defaults to 10.42.0.0/16.
	ClusterCIDR string `json:"clusterCIDR,omitempty"`
	// ServiceCIDR is the CIDR range for the services in the cluster. Defaults to 10.43.0.0/16.
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
	// ClusterDNS is the IP address for the coredns service. Needs to be in the range provided by ServiceCIDR or CoreDNS may not deploy.
	// Defaults to 10.43.0.10.
	ClusterDNS string `json:"clusterDNS,omitempty"`
	// ServerArgs are the ordered key value pairs (e.x. "testArg", "testValue") for the K3s pods running in server mode.
	ServerArgs []string `json:"serverArgs,omitempty"`
	// AgentArgs are the ordered key value pairs (e.x. "testArg", "testValue") for the K3s pods running in agent mode.
	AgentArgs []string `json:"agentArgs,omitempty"`
	// TLSSANs are the subjectAlternativeNames for the certificate the K3s server will use.
	TLSSANs []string `json:"tlsSANs,omitempty"`
	// Addons is a list of secrets containing raw YAML which will be deployed in the virtual K3k cluster on startup.
	Addons []Addon `json:"addons,omitempty"`

	// Persistence contains options controlling how the etcd data of the virtual cluster is persisted. By default, no data
	// persistence is guaranteed, so restart of a virtual cluster pod may result in data loss without this field.
	Persistence *PersistenceConfig `json:"persistence,omitempty"`
	// Expose contains options for exposing the apiserver inside/outside of the cluster. By default, this is only exposed as a
	// clusterIP which is relatively secure, but difficult to access outside of the cluster.
	Expose *ExposeConfig `json:"expose,omitempty"`
}

type Addon struct {
	SecretNamespace string `json:"secretNamespace,omitempty"`
	SecretRef       string `json:"secretRef,omitempty"`
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
	ClusterCIDR string             `json:"clusterCIDR,omitempty"`
	ServiceCIDR string             `json:"serviceCIDR,omitempty"`
	ClusterDNS  string             `json:"clusterDNS,omitempty"`
	TLSSANs     []string           `json:"tlsSANs,omitempty"`
	Persistence *PersistenceConfig `json:"persistence,omitempty"`
}
