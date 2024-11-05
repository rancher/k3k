package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:storageversion
// +kubebuilder:subresource:status

type Cluster struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	metav1.TypeMeta   `json:",inline"`

	Spec   ClusterSpec   `json:"spec"`
	Status ClusterStatus `json:"status,omitempty"`
}

type ClusterSpec struct {
	// Version is a string representing the Kubernetes version to be used by the virtual nodes.
	Version string `json:"version"`
	// +kubebuilder:validation:XValidation:message="cluster must have at least one server",rule="self >= 1"
	// Servers is the number of K3s pods to run in server (controlplane) mode.
	Servers *int32 `json:"servers"`
	// +kubebuilder:validation:XValidation:message="invalid value for agents",rule="self >= 0"
	// Agents is the number of K3s pods to run in agent (worker) mode.
	Agents *int32 `json:"agents"`
	// +optional
	// NodeSelector is the node selector that will be applied to all server/agent pods.
	// In "shared" mode the node selector will be applied also to the workloads.
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// Limit is the limits that apply for the server/worker nodes.
	Limit *ClusterLimit `json:"clusterLimit,omitempty"`
	// +optional
	// TokenSecretRef is Secret reference used as a token join server and worker nodes to the cluster. The controller
	// assumes that the secret has a field "token" in its data, any other fields in the secret will be ignored.
	TokenSecretRef *v1.SecretReference `json:"tokenSecretRef"`
	// +kubebuilder:validation:XValidation:message="clusterCIDR is immutable",rule="self == oldSelf"
	// ClusterCIDR is the CIDR range for the pods of the cluster. Defaults to 10.42.0.0/16.
	ClusterCIDR string `json:"clusterCIDR,omitempty"`
	// +kubebuilder:validation:XValidation:message="serviceCIDR is immutable",rule="self == oldSelf"
	// ServiceCIDR is the CIDR range for the services in the cluster. Defaults to 10.43.0.0/16.
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
	// +kubebuilder:validation:XValidation:message="clusterDNS is immutable",rule="self == oldSelf"
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
	// +kubebuilder:validation:XValidation:message="mode is immutable",rule="self == oldSelf"
	// +kubebuilder:validation:XValidation:message="invalid value for mode",rule="self == \"virtual\" || self == \"shared\""
	// Mode is the cluster provisioning mode which can be either "virtual" or "shared". Defaults to "shared"
	Mode string `json:"mode"`

	// Persistence contains options controlling how the etcd data of the virtual cluster is persisted. By default, no data
	// persistence is guaranteed, so restart of a virtual cluster pod may result in data loss without this field.
	Persistence *PersistenceConfig `json:"persistence,omitempty"`
	// +optional
	// Expose contains options for exposing the apiserver inside/outside of the cluster. By default, this is only exposed as a
	// clusterIP which is relatively secure, but difficult to access outside of the cluster.
	Expose *ExposeConfig `json:"expose,omitempty"`
}

type ClusterLimit struct {
	// ServerLimit is the limits (cpu/mem) that apply to the server nodes
	ServerLimit v1.ResourceList `json:"serverLimit,omitempty"`
	// WorkerLimit is the limits (cpu/mem) that apply to the agent nodes
	WorkerLimit v1.ResourceList `json:"workerLimit,omitempty"`
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
	// +kubebuilder:default="ephemeral"
	Type               string `json:"type"`
	StorageClassName   string `json:"storageClassName,omitempty"`
	StorageRequestSize string `json:"storageRequestSize,omitempty"`
}

type ExposeConfig struct {
	// +optional
	Ingress *IngressConfig `json:"ingress,omitempty"`
	// +optional
	LoadBalancer *LoadBalancerConfig `json:"loadbalancer,omitempty"`
	// +optional
	NodePort *NodePortConfig `json:"nodePort,omitempty"`
}

type IngressConfig struct {
	Enabled          bool   `json:"enabled,omitempty"`
	IngressClassName string `json:"ingressClassName,omitempty"`
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
