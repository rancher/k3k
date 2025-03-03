# API Reference

## Packages
- [k3k.io/v1alpha1](#k3kiov1alpha1)


## k3k.io/v1alpha1


### Resource Types
- [Cluster](#cluster)
- [ClusterList](#clusterlist)



#### Addon



Addon specifies a Secret containing YAML to be deployed on cluster startup.



_Appears in:_
- [ClusterSpec](#clusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `secretNamespace` _string_ | SecretNamespace is the namespace of the Secret. |  |  |
| `secretRef` _string_ | SecretRef is the name of the Secret. |  |  |


#### Cluster



Cluster defines a virtual Kubernetes cluster managed by k3k.
It specifies the desired state of a virtual cluster, including version, node configuration, and networking.
k3k uses this to provision and manage these virtual clusters.



_Appears in:_
- [ClusterList](#clusterlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `k3k.io/v1alpha1` | | |
| `kind` _string_ | `Cluster` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ClusterSpec](#clusterspec)_ | Spec defines the desired state of the Cluster. | \{  \} |  |


#### ClusterLimit



ClusterLimit defines resource limits for server and agent nodes.



_Appears in:_
- [ClusterSpec](#clusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `serverLimit` _[ResourceList](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#resourcelist-v1-core)_ | ServerLimit specifies resource limits for server nodes. |  |  |
| `workerLimit` _[ResourceList](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#resourcelist-v1-core)_ | WorkerLimit specifies resource limits for agent nodes. |  |  |


#### ClusterList



ClusterList is a list of Cluster resources.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `k3k.io/v1alpha1` | | |
| `kind` _string_ | `ClusterList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[Cluster](#cluster) array_ |  |  |  |


#### ClusterMode

_Underlying type:_ _string_

ClusterMode is the possible provisioning mode of a Cluster.

_Validation:_
- Enum: [shared virtual]

_Appears in:_
- [ClusterSpec](#clusterspec)



#### ClusterSpec



ClusterSpec defines the desired state of a virtual Kubernetes cluster.



_Appears in:_
- [Cluster](#cluster)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `version` _string_ | Version is the K3s version to use for the virtual nodes.<br />It should follow the K3s versioning convention (e.g., v1.28.2-k3s1).<br />If not specified, the Kubernetes version of the host node will be used. |  |  |
| `mode` _[ClusterMode](#clustermode)_ | Mode specifies the cluster provisioning mode: "shared" or "virtual".<br />Defaults to "shared". This field is immutable. | shared | Enum: [shared virtual] <br /> |
| `servers` _integer_ | Servers specifies the number of K3s pods to run in server (control plane) mode.<br />Must be at least 1. Defaults to 1. | 1 |  |
| `agents` _integer_ | Agents specifies the number of K3s pods to run in agent (worker) mode.<br />Must be 0 or greater. Defaults to 0.<br />This field is ignored in "shared" mode. | 0 |  |
| `clusterCIDR` _string_ | ClusterCIDR is the CIDR range for pod IPs.<br />Defaults to 10.42.0.0/16 in shared mode and 10.52.0.0/16 in virtual mode.<br />This field is immutable. |  |  |
| `serviceCIDR` _string_ | ServiceCIDR is the CIDR range for service IPs.<br />Defaults to 10.43.0.0/16 in shared mode and 10.53.0.0/16 in virtual mode.<br />This field is immutable. |  |  |
| `clusterDNS` _string_ | ClusterDNS is the IP address for the CoreDNS service.<br />Must be within the ServiceCIDR range. Defaults to 10.43.0.10.<br />This field is immutable. |  |  |
| `persistence` _[PersistenceConfig](#persistenceconfig)_ | Persistence specifies options for persisting etcd data.<br />Defaults to dynamic persistence, which uses a PersistentVolumeClaim to provide data persistence.<br />A default StorageClass is required for dynamic persistence. | \{ type:dynamic \} |  |
| `expose` _[ExposeConfig](#exposeconfig)_ | Expose specifies options for exposing the API server.<br />By default, it's only exposed as a ClusterIP. |  |  |
| `nodeSelector` _object (keys:string, values:string)_ | NodeSelector specifies node labels to constrain where server/agent pods are scheduled.<br />In "shared" mode, this also applies to workloads. |  |  |
| `priorityClass` _string_ | PriorityClass specifies the priorityClassName for server/agent pods.<br />In "shared" mode, this also applies to workloads. |  |  |
| `clusterLimit` _[ClusterLimit](#clusterlimit)_ | Limit defines resource limits for server/agent nodes. |  |  |
| `tokenSecretRef` _[SecretReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#secretreference-v1-core)_ | TokenSecretRef is a Secret reference containing the token used by worker nodes to join the cluster.<br />The Secret must have a "token" field in its data. |  |  |
| `tlsSANs` _string array_ | TLSSANs specifies subject alternative names for the K3s server certificate. |  |  |
| `serverArgs` _string array_ | ServerArgs specifies ordered key-value pairs for K3s server pods.<br />Example: ["--tls-san=example.com"] |  |  |
| `agentArgs` _string array_ | AgentArgs specifies ordered key-value pairs for K3s agent pods.<br />Example: ["--node-name=my-agent-node"] |  |  |
| `addons` _[Addon](#addon) array_ | Addons specifies secrets containing raw YAML to deploy on cluster startup. |  |  |




#### ExposeConfig



ExposeConfig specifies options for exposing the API server.



_Appears in:_
- [ClusterSpec](#clusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ingress` _[IngressConfig](#ingressconfig)_ | Ingress specifies options for exposing the API server through an Ingress. |  |  |
| `loadbalancer` _[LoadBalancerConfig](#loadbalancerconfig)_ | LoadBalancer specifies options for exposing the API server through a LoadBalancer service. |  |  |
| `nodePort` _[NodePortConfig](#nodeportconfig)_ | NodePort specifies options for exposing the API server through NodePort. |  |  |


#### IngressConfig



IngressConfig specifies options for exposing the API server through an Ingress.



_Appears in:_
- [ExposeConfig](#exposeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `annotations` _object (keys:string, values:string)_ | Annotations specifies annotations to add to the Ingress. |  |  |
| `ingressClassName` _string_ | IngressClassName specifies the IngressClass to use for the Ingress. |  |  |


#### LoadBalancerConfig



LoadBalancerConfig specifies options for exposing the API server through a LoadBalancer service.



_Appears in:_
- [ExposeConfig](#exposeconfig)



#### NodePortConfig



NodePortConfig specifies options for exposing the API server through NodePort.



_Appears in:_
- [ExposeConfig](#exposeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `serverPort` _integer_ | ServerPort is the port on each node on which the K3s server service is exposed when type is NodePort.<br />If not specified, a port will be allocated (default: 30000-32767). |  |  |
| `servicePort` _integer_ | ServicePort is the port on each node on which the K3s service is exposed when type is NodePort.<br />If not specified, a port will be allocated (default: 30000-32767). |  |  |
| `etcdPort` _integer_ | ETCDPort is the port on each node on which the ETCD service is exposed when type is NodePort.<br />If not specified, a port will be allocated (default: 30000-32767). |  |  |


#### PersistenceConfig



PersistenceConfig specifies options for persisting etcd data.



_Appears in:_
- [ClusterSpec](#clusterspec)
- [ClusterStatus](#clusterstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[PersistenceMode](#persistencemode)_ | Type specifies the persistence mode. | dynamic |  |
| `storageClassName` _string_ | StorageClassName is the name of the StorageClass to use for the PVC.<br />This field is only relevant in "dynamic" mode. |  |  |
| `storageRequestSize` _string_ | StorageRequestSize is the requested size for the PVC.<br />This field is only relevant in "dynamic" mode. |  |  |


#### PersistenceMode

_Underlying type:_ _string_

PersistenceMode is the storage mode of a Cluster.



_Appears in:_
- [PersistenceConfig](#persistenceconfig)





