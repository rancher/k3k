package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
type Cluster struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	metav1.TypeMeta   `json:",inline"`

	// +kubebuilder:default={}
	// +optional
	Spec   ClusterSpec   `json:"spec"`
	Status ClusterStatus `json:"status,omitempty"`
}

type ClusterSpec struct {
	// Version is a string representing the Kubernetes version to be used by the virtual nodes.
	//
	// +optional
	Version string `json:"version"`

	// Servers is the number of K3s pods to run in server (controlplane) mode.
	//
	// +kubebuilder:default=1
	// +kubebuilder:validation:XValidation:message="cluster must have at least one server",rule="self >= 1"
	// +optional
	Servers *int32 `json:"servers"`

	// Agents is the number of K3s pods to run in agent (worker) mode.
	//
	// +kubebuilder:default=0
	// +kubebuilder:validation:XValidation:message="invalid value for agents",rule="self >= 0"
	// +optional
	Agents *int32 `json:"agents"`

	// NodeSelector is the node selector that will be applied to all server/agent pods.
	// In "shared" mode the node selector will be applied also to the workloads.
	//
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// PriorityClass is the priorityClassName that will be applied to all server/agent pods.
	// In "shared" mode the priorityClassName will be applied also to the workloads.
	PriorityClass string `json:"priorityClass,omitempty"`

	// Limit is the limits that apply for the server/worker nodes.
	Limit *ClusterLimit `json:"clusterLimit,omitempty"`

	// TokenSecretRef is Secret reference used as a token join server and worker nodes to the cluster. The controller
	// assumes that the secret has a field "token" in its data, any other fields in the secret will be ignored.
	// +optional
	TokenSecretRef *v1.SecretReference `json:"tokenSecretRef"`

	// ClusterCIDR is the CIDR range for the pods of the cluster. Defaults to 10.42.0.0/16.
	// +kubebuilder:validation:XValidation:message="clusterCIDR is immutable",rule="self == oldSelf"
	ClusterCIDR string `json:"clusterCIDR,omitempty"`

	// ServiceCIDR is the CIDR range for the services in the cluster. Defaults to 10.43.0.0/16.
	// +kubebuilder:validation:XValidation:message="serviceCIDR is immutable",rule="self == oldSelf"
	ServiceCIDR string `json:"serviceCIDR,omitempty"`

	// ClusterDNS is the IP address for the coredns service. Needs to be in the range provided by ServiceCIDR or CoreDNS may not deploy.
	// Defaults to 10.43.0.10.
	// +kubebuilder:validation:XValidation:message="clusterDNS is immutable",rule="self == oldSelf"
	ClusterDNS string `json:"clusterDNS,omitempty"`

	// ServerArgs are the ordered key value pairs (e.x. "testArg", "testValue") for the K3s pods running in server mode.
	ServerArgs []string `json:"serverArgs,omitempty"`

	// AgentArgs are the ordered key value pairs (e.x. "testArg", "testValue") for the K3s pods running in agent mode.
	AgentArgs []string `json:"agentArgs,omitempty"`

	// TLSSANs are the subjectAlternativeNames for the certificate the K3s server will use.
	TLSSANs []string `json:"tlsSANs,omitempty"`

	// Addons is a list of secrets containing raw YAML which will be deployed in the virtual K3k cluster on startup.
	Addons []Addon `json:"addons,omitempty"`

	// Mode is the cluster provisioning mode which can be either "shared" or "virtual". Defaults to "shared"
	//
	// +kubebuilder:default="shared"
	// +kubebuilder:validation:Enum=shared;virtual
	// +kubebuilder:validation:XValidation:message="mode is immutable",rule="self == oldSelf"
	// +optional
	Mode ClusterMode `json:"mode,omitempty"`

	// Persistence contains options controlling how the etcd data of the virtual cluster is persisted. By default, no data
	// persistence is guaranteed, so restart of a virtual cluster pod may result in data loss without this field.
	// +kubebuilder:default={type: "dynamic"}
	Persistence PersistenceConfig `json:"persistence,omitempty"`

	// Expose contains options for exposing the apiserver inside/outside of the cluster. By default, this is only exposed as a
	// clusterIP which is relatively secure, but difficult to access outside of the cluster.
	// +optional
	Expose *ExposeConfig `json:"expose,omitempty"`
}

// +kubebuilder:validation:Enum=shared;virtual
// +kubebuilder:default="shared"
//
// ClusterMode is the possible provisioning mode of a Cluster.
type ClusterMode string

// +kubebuilder:default="dynamic"
//
// PersistenceMode is the storage mode of a Cluster.
type PersistenceMode string

const (
	SharedClusterMode  = ClusterMode("shared")
	VirtualClusterMode = ClusterMode("virtual")
	EphemeralNodeType  = PersistenceMode("ephemeral")
	DynamicNodesType   = PersistenceMode("dynamic")
)

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
// +kubebuilder:object:root=true
type ClusterList struct {
	metav1.ListMeta `json:"metadata,omitempty"`
	metav1.TypeMeta `json:",inline"`

	Items []Cluster `json:"items"`
}

type PersistenceConfig struct {
	// +kubebuilder:default="dynamic"
	Type               PersistenceMode `json:"type"`
	StorageClassName   *string         `json:"storageClassName,omitempty"`
	StorageRequestSize string          `json:"storageRequestSize,omitempty"`
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
	// Annotations is a key value map that will enrich the Ingress annotations
	// +optional
	Annotations      map[string]string `json:"annotations,omitempty"`
	IngressClassName string            `json:"ingressClassName,omitempty"`
}

type LoadBalancerConfig struct {
	Enabled bool `json:"enabled"`
}

type NodePortConfig struct {
	// ServerPort is the port on each node on which the K3s server service is exposed when type is NodePort.
	// If not specified, a port will be allocated (default: 30000-32767)
	// +optional
	ServerPort *int32 `json:"serverPort,omitempty"`
	// ServicePort is the port on each node on which the K3s service is exposed when type is NodePort.
	// If not specified, a port will be allocated (default: 30000-32767)
	// +optional
	ServicePort *int32 `json:"servicePort,omitempty"`
	// ETCDPort is the port on each node on which the ETCD service is exposed when type is NodePort.
	// If not specified, a port will be allocated (default: 30000-32767)
	// +optional
	ETCDPort *int32 `json:"etcdPort,omitempty"`
}

type ClusterStatus struct {
	HostVersion string            `json:"hostVersion,omitempty"`
	ClusterCIDR string            `json:"clusterCIDR,omitempty"`
	ServiceCIDR string            `json:"serviceCIDR,omitempty"`
	ClusterDNS  string            `json:"clusterDNS,omitempty"`
	TLSSANs     []string          `json:"tlsSANs,omitempty"`
	Persistence PersistenceConfig `json:"persistence,omitempty"`
}
