package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:storageversion
// +kubebuilder:subresource:status

type ClusterSet struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	metav1.TypeMeta   `json:",inline"`

	// Spec is the spec of the ClusterSet
	Spec ClusterSetSpec `json:"spec"`
	// Status is the status of the ClusterSet
	Status ClusterSetStatus `json:"status,omitempty"`
}

type ClusterSetSpec struct {
	// MaxLimits are the limits that apply to all clusters (server + agent) in the set
	MaxLimits v1.ResourceList `json:"maxLimits,omitempty"`

	// DefaultLimits are the limits used for servers/agents when a cluster in the set doesn't provide any
	DefaultLimits *ClusterLimit `json:"defaultLimits,omitempty"`

	// DefaultNodeSelector is the node selector that applies to all clusters (server + agent) in the set
	DefaultNodeSelector map[string]string `json:"defaultNodeSelector,omitempty"`

	// DisableNetworkPolicy is an option that will disable the creation of a default networkpolicy for cluster isolation
	DisableNetworkPolicy bool `json:"disableNetworkPolicy,omitempty"`

	// Mode is the cluster provisioning mode that applies to all the clusters.
	// It can be either "virtual" or "shared". Defaults to "shared".
	// +kubebuilder:validation:XValidation:message="mode is immutable",rule="self == oldSelf"
	// +kubebuilder:validation:XValidation:message="invalid value for mode",rule="self == \"virtual\" || self == \"shared\""
	Mode string `json:"mode"`
}

type ClusterSetStatus struct {
	// ObservedGeneration was the generation at the time the status was updated.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// LastUpdate is the timestamp when the status was last updated
	LastUpdate string `json:"lastUpdateTime,omitempty"`
	// Sumamry is a summary of the status (error, ready)
	Summary string `json:"summary,omitempty"`
	// Conditions are the invidual conditions for the cluster set
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ClusterSetList struct {
	metav1.ListMeta `json:"metadata,omitempty"`
	metav1.TypeMeta `json:",inline"`

	Items []ClusterSet `json:"items"`
}
