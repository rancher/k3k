package v1beta1

import (
	"k8s.io/apimachinery/pkg/api/resource"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.mode",name=Mode,type=string
// +kubebuilder:printcolumn:JSONPath=".status.phase",name="Status",type="string"
// +kubebuilder:printcolumn:JSONPath=".status.policyName",name=Policy,type=string

// Cluster defines a virtual Kubernetes cluster managed by k3k.
// It specifies the desired state of a virtual cluster, including version, node configuration, and networking.
// k3k uses this to provision and manage these virtual clusters.
type Cluster struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	metav1.TypeMeta   `json:",inline"`

	// Spec defines the desired state of the Cluster.
	//
	// +kubebuilder:default={}
	// +optional
	Spec ClusterSpec `json:"spec"`

	// Status reflects the observed state of the Cluster.
	//
	// +kubebuilder:default={}
	// +optional
	Status ClusterStatus `json:"status,omitempty"`
}

// ClusterSpec defines the desired state of a virtual Kubernetes cluster.
type ClusterSpec struct {
	// Version is the K3s version to use for the virtual nodes.
	// It should follow the K3s versioning convention (e.g., v1.28.2-k3s1).
	// If not specified, the Kubernetes version of the host node will be used.
	//
	// +optional
	Version string `json:"version,omitempty"`

	// Mode specifies the cluster provisioning mode: "shared" or "virtual".
	// Defaults to "shared". This field is immutable.
	//
	// +kubebuilder:default="shared"
	// +kubebuilder:validation:Enum=shared;virtual
	// +kubebuilder:validation:XValidation:message="mode is immutable",rule="self == oldSelf"
	// +optional
	Mode ClusterMode `json:"mode,omitempty"`

	// Servers specifies the number of K3s pods to run in server (control plane) mode.
	// Must be at least 1. Defaults to 1.
	//
	// +kubebuilder:validation:XValidation:message="cluster must have at least one server",rule="self >= 1"
	// +kubebuilder:default=1
	// +optional
	Servers *int32 `json:"servers"`

	// Agents specifies the number of K3s pods to run in agent (worker) mode.
	// Must be 0 or greater. Defaults to 0.
	// This field is ignored in "shared" mode.
	//
	// +kubebuilder:default=0
	// +kubebuilder:validation:XValidation:message="invalid value for agents",rule="self >= 0"
	// +optional
	Agents *int32 `json:"agents"`

	// ClusterCIDR is the CIDR range for pod IPs.
	// Defaults to 10.42.0.0/16 in shared mode and 10.52.0.0/16 in virtual mode.
	// This field is immutable.
	//
	// +kubebuilder:validation:XValidation:message="clusterCIDR is immutable",rule="self == oldSelf"
	// +optional
	ClusterCIDR string `json:"clusterCIDR,omitempty"`

	// ServiceCIDR is the CIDR range for service IPs.
	// Defaults to 10.43.0.0/16 in shared mode and 10.53.0.0/16 in virtual mode.
	// This field is immutable.
	//
	// +kubebuilder:validation:XValidation:message="serviceCIDR is immutable",rule="self == oldSelf"
	// +optional
	ServiceCIDR string `json:"serviceCIDR,omitempty"`

	// ClusterDNS is the IP address for the CoreDNS service.
	// Must be within the ServiceCIDR range. Defaults to 10.43.0.10.
	// This field is immutable.
	//
	// +kubebuilder:validation:XValidation:message="clusterDNS is immutable",rule="self == oldSelf"
	// +optional
	ClusterDNS string `json:"clusterDNS,omitempty"`

	// Persistence specifies options for persisting etcd data.
	// Defaults to dynamic persistence, which uses a PersistentVolumeClaim to provide data persistence.
	// A default StorageClass is required for dynamic persistence.
	//
	// +optional
	Persistence PersistenceConfig `json:"persistence"`

	// Expose specifies options for exposing the API server.
	// By default, it's only exposed as a ClusterIP.
	//
	// +kubebuilder:validation:XValidation:rule="[has(self.ingress), has(self.loadBalancer), has(self.nodePort)].filter(x, x).size() <= 1",message="ingress, loadbalancer and nodePort are mutually exclusive; only one can be set"
	// +optional
	Expose *ExposeConfig `json:"expose,omitempty"`

	// NodeSelector specifies node labels to constrain where server/agent pods are scheduled.
	// In "shared" mode, this also applies to workloads.
	//
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// PriorityClass specifies the priorityClassName for server/agent pods.
	// In "shared" mode, this also applies to workloads.
	//
	// +optional
	PriorityClass string `json:"priorityClass,omitempty"`

	// TokenSecretRef is a Secret reference containing the token used by worker nodes to join the cluster.
	// The Secret must have a "token" field in its data.
	//
	// +optional
	TokenSecretRef *corev1.SecretReference `json:"tokenSecretRef,omitempty"`

	// TLSSANs specifies subject alternative names for the K3s server certificate.
	//
	// +optional
	TLSSANs []string `json:"tlsSANs,omitempty"`

	// ServerArgs specifies ordered key-value pairs for K3s server pods.
	// Example: ["--tls-san=example.com"]
	//
	// +optional
	ServerArgs []string `json:"serverArgs,omitempty"`

	// AgentArgs specifies ordered key-value pairs for K3s agent pods.
	// Example: ["--node-name=my-agent-node"]
	//
	// +optional
	AgentArgs []string `json:"agentArgs,omitempty"`

	// ServerEnvs specifies list of environment variables to set in the server pod.
	//
	// +optional
	ServerEnvs []corev1.EnvVar `json:"serverEnvs,omitempty"`

	// AgentEnvs specifies list of environment variables to set in the agent pod.
	//
	// +optional
	AgentEnvs []corev1.EnvVar `json:"agentEnvs,omitempty"`

	// Addons specifies secrets containing raw YAML to deploy on cluster startup.
	//
	// +optional
	Addons []Addon `json:"addons,omitempty"`

	// ServerLimit specifies resource limits for server nodes.
	//
	// +optional
	ServerLimit corev1.ResourceList `json:"serverLimit,omitempty"`

	// WorkerLimit specifies resource limits for agent nodes.
	//
	// +optional
	WorkerLimit corev1.ResourceList `json:"workerLimit,omitempty"`

	// MirrorHostNodes controls whether node objects from the host cluster
	// are mirrored into the virtual cluster.
	//
	// +optional
	MirrorHostNodes bool `json:"mirrorHostNodes,omitempty"`

	// CustomCAs specifies the cert/key pairs for custom CA certificates.
	//
	// +optional
	CustomCAs *CustomCAs `json:"customCAs,omitempty"`

	// Sync specifies the resources types that will be synced from virtual cluster to host cluster.
	//
	// +kubebuilder:default={}
	// +optional
	Sync *SyncConfig `json:"sync,omitempty"`

	// SecretMounts specifies a list of secrets to mount into server and agent pods.
	// Each entry defines a secret and its mount path within the pods.
	//
	// +optional
	SecretMounts []SecretMount `json:"secretMounts,omitempty"`
}

// SecretMount represent any extra mount that the user will specify
type SecretMount struct {
	// Embeds SecretName, Items, DefaultMode, and Optional
	v1.SecretVolumeSource `json:",inline"`
	// MountPath is the path within server and agent pods where the
	// secret contents will be mounted.
	//
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// SubPath is an optional path within the secret to mount instead of the root.
	// When specified, only the specified key from the secret will be mounted as a file
	// at MountPath, keeping the parent directory writable.
	//
	// +optional
	SubPath string `json:"subPath,omitempty"`
}

// SyncConfig will contain the resources that should be synced from virtual cluster to host cluster.
type SyncConfig struct {
	// Services resources sync configuration.
	//
	// +kubebuilder:default={"enabled": true}
	// +optional
	Services ServiceSyncConfig `json:"services"`
	// ConfigMaps resources sync configuration.
	//
	// +kubebuilder:default={"enabled": true}
	// +optional
	ConfigMaps ConfigMapSyncConfig `json:"configMaps"`
	// Secrets resources sync configuration.
	//
	// +kubebuilder:default={"enabled": true}
	// +optional
	Secrets SecretSyncConfig `json:"secrets"`
	// Ingresses resources sync configuration.
	//
	// +kubebuilder:default={"enabled": false}
	// +optional
	Ingresses IngressSyncConfig `json:"ingresses"`
	// PersistentVolumeClaims resources sync configuration.
	//
	// +kubebuilder:default={"enabled": true}
	// +optional
	PersistentVolumeClaims PersistentVolumeClaimSyncConfig `json:"persistentVolumeClaims"`
	// PriorityClasses resources sync configuration.
	//
	// +kubebuilder:default={"enabled": false}
	// +optional
	PriorityClasses PriorityClassSyncConfig `json:"priorityClasses"`
}

// SecretSyncConfig specifies the sync options for services.
type SecretSyncConfig struct {
	// Enabled is an on/off switch for syncing resources.
	//
	// +kubebuilder:default=true
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Selector specifies set of labels of the resources that will be synced, if empty
	// then all resources of the given type will be synced.
	//
	// +optional
	Selector map[string]string `json:"selector,omitempty"`
}

// ServiceSyncConfig specifies the sync options for services.
type ServiceSyncConfig struct {
	// Enabled is an on/off switch for syncing resources.
	//
	// +kubebuilder:default=true
	// +required
	Enabled bool `json:"enabled"`

	// Selector specifies set of labels of the resources that will be synced, if empty
	// then all resources of the given type will be synced.
	//
	// +optional
	Selector map[string]string `json:"selector,omitempty"`
}

// ConfigMapSyncConfig specifies the sync options for services.
type ConfigMapSyncConfig struct {
	// Enabled is an on/off switch for syncing resources.
	//
	// +kubebuilder:default=true
	// +required
	Enabled bool `json:"enabled"`

	// Selector specifies set of labels of the resources that will be synced, if empty
	// then all resources of the given type will be synced.
	//
	// +optional
	Selector map[string]string `json:"selector,omitempty"`
}

// IngressSyncConfig specifies the sync options for services.
type IngressSyncConfig struct {
	// Enabled is an on/off switch for syncing resources.
	//
	// +kubebuilder:default=false
	// +required
	Enabled bool `json:"enabled"`

	// Selector specifies set of labels of the resources that will be synced, if empty
	// then all resources of the given type will be synced.
	//
	// +optional
	Selector map[string]string `json:"selector,omitempty"`
}

// PersistentVolumeClaimSyncConfig specifies the sync options for services.
type PersistentVolumeClaimSyncConfig struct {
	// Enabled is an on/off switch for syncing resources.
	//
	// +kubebuilder:default=true
	// +required
	Enabled bool `json:"enabled"`

	// Selector specifies set of labels of the resources that will be synced, if empty
	// then all resources of the given type will be synced.
	//
	// +optional
	Selector map[string]string `json:"selector,omitempty"`
}

// PriorityClassSyncConfig specifies the sync options for services.
type PriorityClassSyncConfig struct {
	// Enabled is an on/off switch for syncing resources.
	//
	// +kubebuilder:default=false
	// +required
	Enabled bool `json:"enabled"`

	// Selector specifies set of labels of the resources that will be synced, if empty
	// then all resources of the given type will be synced.
	//
	// +optional
	Selector map[string]string `json:"selector,omitempty"`
}

// ClusterMode is the possible provisioning mode of a Cluster.
//
// +kubebuilder:validation:Enum=shared;virtual
// +kubebuilder:default="shared"
type ClusterMode string

const (
	// SharedClusterMode represents a cluster that shares resources with the host node.
	SharedClusterMode = ClusterMode("shared")

	// VirtualClusterMode represents a cluster that runs in a virtual environment.
	VirtualClusterMode = ClusterMode("virtual")
)

// PersistenceMode is the storage mode of a Cluster.
//
// +kubebuilder:default="dynamic"
type PersistenceMode string

const (
	// EphemeralPersistenceMode represents a cluster with no data persistence.
	EphemeralPersistenceMode = PersistenceMode("ephemeral")

	// DynamicPersistenceMode represents a cluster with dynamic data persistence using a PVC.
	DynamicPersistenceMode = PersistenceMode("dynamic")
)

// Addon specifies a Secret containing YAML to be deployed on cluster startup.
type Addon struct {
	// SecretNamespace is the namespace of the Secret.
	SecretNamespace string `json:"secretNamespace,omitempty"`

	// SecretRef is the name of the Secret.
	SecretRef string `json:"secretRef,omitempty"`
}

// PersistenceConfig specifies options for persisting etcd data.
type PersistenceConfig struct {
	// Type specifies the persistence mode.
	//
	// +kubebuilder:default="dynamic"
	Type PersistenceMode `json:"type,omitempty"`

	// StorageClassName is the name of the StorageClass to use for the PVC.
	// This field is only relevant in "dynamic" mode.
	//
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// StorageRequestSize is the requested size for the PVC.
	// This field is only relevant in "dynamic" mode.
	//
	// +kubebuilder:default="2G"
	// +kubebuilder:validation:XValidation:message="storageRequestSize is immutable",rule="self == oldSelf"
	// +optional
	StorageRequestSize *resource.Quantity `json:"storageRequestSize,omitempty"`
}

// ExposeConfig specifies options for exposing the API server.
type ExposeConfig struct {
	// Ingress specifies options for exposing the API server through an Ingress.
	//
	// +optional
	Ingress *IngressConfig `json:"ingress,omitempty"`

	// LoadBalancer specifies options for exposing the API server through a LoadBalancer service.
	//
	// +optional
	LoadBalancer *LoadBalancerConfig `json:"loadBalancer,omitempty"`

	// NodePort specifies options for exposing the API server through NodePort.
	//
	// +optional
	NodePort *NodePortConfig `json:"nodePort,omitempty"`
}

// IngressConfig specifies options for exposing the API server through an Ingress.
type IngressConfig struct {
	// Annotations specifies annotations to add to the Ingress.
	//
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// IngressClassName specifies the IngressClass to use for the Ingress.
	//
	// +optional
	IngressClassName string `json:"ingressClassName,omitempty"`
}

// LoadBalancerConfig specifies options for exposing the API server through a LoadBalancer service.
type LoadBalancerConfig struct {
	// ServerPort is the port on which the K3s server is exposed when type is LoadBalancer.
	// If not specified, the default https 443 port will be allocated.
	// If 0 or negative, the port will not be exposed.
	//
	// +optional
	ServerPort *int32 `json:"serverPort,omitempty"`

	// ETCDPort is the port on which the ETCD service is exposed when type is LoadBalancer.
	// If not specified, the default etcd 2379 port will be allocated.
	// If 0 or negative, the port will not be exposed.
	//
	// +optional
	ETCDPort *int32 `json:"etcdPort,omitempty"`
}

// NodePortConfig specifies options for exposing the API server through NodePort.
type NodePortConfig struct {
	// ServerPort is the port on each node on which the K3s server is exposed when type is NodePort.
	// If not specified, a random port between 30000-32767 will be allocated.
	// If out of range, the port will not be exposed.
	//
	// +optional
	ServerPort *int32 `json:"serverPort,omitempty"`

	// ETCDPort is the port on each node on which the ETCD service is exposed when type is NodePort.
	// If not specified, a random port between 30000-32767 will be allocated.
	// If out of range, the port will not be exposed.
	//
	// +optional
	ETCDPort *int32 `json:"etcdPort,omitempty"`
}

// CustomCAs specifies the cert/key pairs for custom CA certificates.
type CustomCAs struct {
	// Enabled toggles this feature on or off.
	//
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Sources defines the sources for all required custom CA certificates.
	Sources CredentialSources `json:"sources"`
}

// CredentialSources lists all the required credentials, including both
// TLS key pairs and single signing keys.
type CredentialSources struct {
	// ServerCA specifies the server-ca cert/key pair.
	ServerCA CredentialSource `json:"serverCA"`

	// ClientCA specifies the client-ca cert/key pair.
	ClientCA CredentialSource `json:"clientCA"`

	// RequestHeaderCA specifies the request-header-ca cert/key pair.
	RequestHeaderCA CredentialSource `json:"requestHeaderCA"`

	// ETCDServerCA specifies the etcd-server-ca cert/key pair.
	ETCDServerCA CredentialSource `json:"etcdServerCA"`

	// ETCDPeerCA specifies the etcd-peer-ca cert/key pair.
	ETCDPeerCA CredentialSource `json:"etcdPeerCA"`

	// ServiceAccountToken specifies the service-account-token key.
	ServiceAccountToken CredentialSource `json:"serviceAccountToken"`
}

// CredentialSource defines where to get a credential from.
// It can represent either a TLS key pair or a single private key.
type CredentialSource struct {
	// SecretName specifies the name of an existing secret to use.
	// The custom certificate controller expects specific keys inside based on the credential type:
	// - For TLS pairs (e.g., ServerCA): 'tls.crt' and 'tls.key'.
	// - For ServiceAccountTokenKey: 'tls.key'.
	SecretName string `json:"secretName"`
}

// ClusterStatus reflects the observed state of a Cluster.
type ClusterStatus struct {
	// HostVersion is the Kubernetes version of the host node.
	//
	// +optional
	HostVersion string `json:"hostVersion,omitempty"`

	// ClusterCIDR is the CIDR range for pod IPs.
	//
	// +optional
	ClusterCIDR string `json:"clusterCIDR,omitempty"`

	// ServiceCIDR is the CIDR range for service IPs.
	//
	// +optional
	ServiceCIDR string `json:"serviceCIDR,omitempty"`

	// ClusterDNS is the IP address for the CoreDNS service.
	//
	// +optional
	ClusterDNS string `json:"clusterDNS,omitempty"`

	// TLSSANs specifies subject alternative names for the K3s server certificate.
	//
	// +optional
	TLSSANs []string `json:"tlsSANs,omitempty"`

	// PolicyName specifies the virtual cluster policy name bound to the virtual cluster.
	//
	// +optional
	PolicyName string `json:"policyName,omitempty"`

	// KubeletPort specefies the port used by k3k-kubelet in shared mode.
	//
	// +optional
	KubeletPort int `json:"kubeletPort,omitempty"`

	// WebhookPort specefies the port used by webhook in k3k-kubelet in shared mode.
	//
	// +optional
	WebhookPort int `json:"webhookPort,omitempty"`

	// Conditions are the individual conditions for the cluster set.
	//
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase is a high-level summary of the cluster's current lifecycle state.
	//
	// +kubebuilder:default="Unknown"
	// +kubebuilder:validation:Enum=Pending;Provisioning;Ready;Failed;Terminating;Unknown
	// +optional
	Phase ClusterPhase `json:"phase,omitempty"`
}

// ClusterPhase is a high-level summary of the cluster's current lifecycle state.
type ClusterPhase string

const (
	ClusterPending      = ClusterPhase("Pending")
	ClusterProvisioning = ClusterPhase("Provisioning")
	ClusterReady        = ClusterPhase("Ready")
	ClusterFailed       = ClusterPhase("Failed")
	ClusterTerminating  = ClusterPhase("Terminating")
	ClusterUnknown      = ClusterPhase("Unknown")
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// ClusterList is a list of Cluster resources.
type ClusterList struct {
	metav1.ListMeta `json:"metadata,omitempty"`
	metav1.TypeMeta `json:",inline"`

	Items []Cluster `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:JSONPath=".spec.allowedMode",name=Mode,type=string
// +kubebuilder:resource:scope=Cluster,shortName=vcp

// VirtualClusterPolicy allows defining common configurations and constraints
// for clusters within a clusterpolicy.
type VirtualClusterPolicy struct {
	metav1.ObjectMeta `json:"metadata"`
	metav1.TypeMeta   `json:",inline"`

	// Spec defines the desired state of the VirtualClusterPolicy.
	//
	// +kubebuilder:default={}
	Spec VirtualClusterPolicySpec `json:"spec"`

	// Status reflects the observed state of the VirtualClusterPolicy.
	//
	// +optional
	Status VirtualClusterPolicyStatus `json:"status"`
}

// VirtualClusterPolicySpec defines the desired state of a VirtualClusterPolicy.
type VirtualClusterPolicySpec struct {
	// Quota specifies the resource limits for clusters within a clusterpolicy.
	//
	// +optional
	Quota *corev1.ResourceQuotaSpec `json:"quota,omitempty"`

	// Limit specifies the LimitRange that will be applied to all pods within the VirtualClusterPolicy
	// to set defaults and constraints (min/max)
	//
	// +optional
	Limit *corev1.LimitRangeSpec `json:"limit,omitempty"`

	// DefaultNodeSelector specifies the node selector that applies to all clusters (server + agent) in the target Namespace.
	//
	// +optional
	DefaultNodeSelector map[string]string `json:"defaultNodeSelector,omitempty"`

	// DefaultPriorityClass specifies the priorityClassName applied to all pods of all clusters in the target Namespace.
	//
	// +optional
	DefaultPriorityClass string `json:"defaultPriorityClass,omitempty"`

	// AllowedMode specifies the allowed cluster provisioning mode. Defaults to "shared".
	//
	// +kubebuilder:default=shared
	// +kubebuilder:validation:XValidation:message="mode is immutable",rule="self == oldSelf"
	// +optional
	AllowedMode ClusterMode `json:"allowedMode,omitempty"`

	// DisableNetworkPolicy indicates whether to disable the creation of a default network policy for cluster isolation.
	//
	// +optional
	DisableNetworkPolicy bool `json:"disableNetworkPolicy,omitempty"`

	// PodSecurityAdmissionLevel specifies the pod security admission level applied to the pods in the namespace.
	//
	// +optional
	PodSecurityAdmissionLevel *PodSecurityAdmissionLevel `json:"podSecurityAdmissionLevel,omitempty"`

	// Sync specifies the resources types that will be synced from virtual cluster to host cluster.
	//
	// +kubebuilder:default={}
	// +optional
	Sync *SyncConfig `json:"sync,omitempty"`
}

// PodSecurityAdmissionLevel is the policy level applied to the pods in the namespace.
//
// +kubebuilder:validation:Enum=privileged;baseline;restricted
type PodSecurityAdmissionLevel string

const (
	// PrivilegedPodSecurityAdmissionLevel allows all pods to be admitted.
	PrivilegedPodSecurityAdmissionLevel = PodSecurityAdmissionLevel("privileged")

	// BaselinePodSecurityAdmissionLevel enforces a baseline level of security restrictions.
	BaselinePodSecurityAdmissionLevel = PodSecurityAdmissionLevel("baseline")

	// RestrictedPodSecurityAdmissionLevel enforces stricter security restrictions.
	RestrictedPodSecurityAdmissionLevel = PodSecurityAdmissionLevel("restricted")
)

// VirtualClusterPolicyStatus reflects the observed state of a VirtualClusterPolicy.
type VirtualClusterPolicyStatus struct {
	// ObservedGeneration was the generation at the time the status was updated.
	//
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastUpdate is the timestamp when the status was last updated.
	//
	// +optional
	LastUpdate string `json:"lastUpdateTime,omitempty"`

	// Summary is a summary of the status.
	//
	// +optional
	Summary string `json:"summary,omitempty"`

	// Conditions are the individual conditions for the cluster set.
	//
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// VirtualClusterPolicyList is a list of VirtualClusterPolicy resources.
type VirtualClusterPolicyList struct {
	metav1.ListMeta `json:"metadata,omitempty"`
	metav1.TypeMeta `json:",inline"`

	Items []VirtualClusterPolicy `json:"items"`
}
