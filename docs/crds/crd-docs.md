# API Reference

## Packages
- [k3k.io/v1alpha1](#k3kiov1alpha1)


## k3k.io/v1alpha1


### Resource Types
- [Cluster](#cluster)
- [ClusterList](#clusterlist)



#### Addon







_Appears in:_
- [ClusterSpec](#clusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `secretNamespace` _string_ |  |  |  |
| `secretRef` _string_ |  |  |  |


#### Cluster







_Appears in:_
- [ClusterList](#clusterlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `k3k.io/v1alpha1` | | |
| `kind` _string_ | `Cluster` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ClusterSpec](#clusterspec)_ |  | \{  \} |  |


#### ClusterLimit







_Appears in:_
- [ClusterSpec](#clusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `serverLimit` _[ResourceList](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#resourcelist-v1-core)_ | ServerLimit is the limits (cpu/mem) that apply to the server nodes |  |  |
| `workerLimit` _[ResourceList](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#resourcelist-v1-core)_ | WorkerLimit is the limits (cpu/mem) that apply to the agent nodes |  |  |


#### ClusterList









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







_Appears in:_
- [Cluster](#cluster)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `version` _string_ | Version is a string representing the Kubernetes version to be used by the virtual nodes. |  |  |
| `servers` _integer_ | Servers is the number of K3s pods to run in server (controlplane) mode. | 1 |  |
| `agents` _integer_ | Agents is the number of K3s pods to run in agent (worker) mode. | 0 |  |
| `nodeSelector` _object (keys:string, values:string)_ | NodeSelector is the node selector that will be applied to all server/agent pods.<br />In "shared" mode the node selector will be applied also to the workloads. |  |  |
| `priorityClass` _string_ | PriorityClass is the priorityClassName that will be applied to all server/agent pods.<br />In "shared" mode the priorityClassName will be applied also to the workloads. |  |  |
| `clusterLimit` _[ClusterLimit](#clusterlimit)_ | Limit is the limits that apply for the server/worker nodes. |  |  |
| `tokenSecretRef` _[SecretReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#secretreference-v1-core)_ | TokenSecretRef is Secret reference used as a token join server and worker nodes to the cluster. The controller<br />assumes that the secret has a field "token" in its data, any other fields in the secret will be ignored. |  |  |
| `clusterCIDR` _string_ | ClusterCIDR is the CIDR range for the pods of the cluster. Defaults to 10.42.0.0/16. |  |  |
| `serviceCIDR` _string_ | ServiceCIDR is the CIDR range for the services in the cluster. Defaults to 10.43.0.0/16. |  |  |
| `clusterDNS` _string_ | ClusterDNS is the IP address for the coredns service. Needs to be in the range provided by ServiceCIDR or CoreDNS may not deploy.<br />Defaults to 10.43.0.10. |  |  |
| `serverArgs` _string array_ | ServerArgs are the ordered key value pairs (e.x. "testArg", "testValue") for the K3s pods running in server mode. |  |  |
| `agentArgs` _string array_ | AgentArgs are the ordered key value pairs (e.x. "testArg", "testValue") for the K3s pods running in agent mode. |  |  |
| `tlsSANs` _string array_ | TLSSANs are the subjectAlternativeNames for the certificate the K3s server will use. |  |  |
| `addons` _[Addon](#addon) array_ | Addons is a list of secrets containing raw YAML which will be deployed in the virtual K3k cluster on startup. |  |  |
| `mode` _[ClusterMode](#clustermode)_ | Mode is the cluster provisioning mode which can be either "shared" or "virtual". Defaults to "shared" | shared | Enum: [shared virtual] <br /> |
| `persistence` _[PersistenceConfig](#persistenceconfig)_ | Persistence contains options controlling how the etcd data of the virtual cluster is persisted. By default, no data<br />persistence is guaranteed, so restart of a virtual cluster pod may result in data loss without this field. |  |  |
| `expose` _[ExposeConfig](#exposeconfig)_ | Expose contains options for exposing the apiserver inside/outside of the cluster. By default, this is only exposed as a<br />clusterIP which is relatively secure, but difficult to access outside of the cluster. |  |  |




#### ExposeConfig







_Appears in:_
- [ClusterSpec](#clusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ingress` _[IngressConfig](#ingressconfig)_ |  |  |  |
| `loadbalancer` _[LoadBalancerConfig](#loadbalancerconfig)_ |  |  |  |
| `nodePort` _[NodePortConfig](#nodeportconfig)_ |  |  |  |


#### IngressConfig







_Appears in:_
- [ExposeConfig](#exposeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ |  |  |  |
| `ingressClassName` _string_ |  |  |  |


#### LoadBalancerConfig







_Appears in:_
- [ExposeConfig](#exposeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ |  |  |  |


#### NodePortConfig







_Appears in:_
- [ExposeConfig](#exposeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `serverPort` _integer_ | ServerPort is the port on each node on which the K3s server service is exposed when type is NodePort.<br />If not specified, a port will be allocated (default: 30000-32767) |  |  |
| `servicePort` _integer_ | ServicePort is the port on each node on which the K3s service is exposed when type is NodePort.<br />If not specified, a port will be allocated (default: 30000-32767) |  |  |
| `etcdPort` _integer_ | ETCDPort is the port on each node on which the ETCD service is exposed when type is NodePort.<br />If not specified, a port will be allocated (default: 30000-32767) |  |  |


#### PersistenceConfig







_Appears in:_
- [ClusterSpec](#clusterspec)
- [ClusterStatus](#clusterstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type can be ephemeral, static, dynamic | ephemeral |  |
| `storageClassName` _string_ |  |  |  |
| `storageRequestSize` _string_ |  |  |  |




