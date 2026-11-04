# VirtualClusterPolicy

The VirtualClusterPolicy Custom Resource in K3k provides a way to define and enforce consistent configurations, security settings, and resource management rules for your virtual clusters and the Namespaces they operate within.

By using VCPs, administrators can centrally manage these aspects, reducing manual configuration, ensuring alignment with organizational standards, and enhancing the overall security and operational consistency of the K3k environment.

## Core Concepts

### What is a VirtualClusterPolicy?

A `VirtualClusterPolicy` is a cluster-scoped Kubernetes Custom Resource that specifies a set of rules and configurations. These policies are then applied to K3k virtual clusters (`Cluster` resources) operating within Kubernetes Namespaces that are explicitly bound to a VCP.

### Binding a Policy to a Namespace

To apply a `VirtualClusterPolicy` to one or more Namespaces (and thus to all K3k `Cluster` resources within those Namespaces), you need to label the desired Namespace(s). Add the following label to your Namespace metadata:

`policy.k3k.io/policy-name: <YOUR_POLICY_NAME>`

**Example: Labeling a Namespace**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-app-namespace
  labels:
    policy.k3k.io/policy-name: "standard-dev-policy"
```

In this example, `my-app-namespace` will adhere to the rules defined in the `VirtualClusterPolicy` named `standard-dev-policy`. Multiple Namespaces can be bound to the same policy for uniform configuration, or different Namespaces can be bound to distinct policies.

It's also important to note what happens when a Namespace's policy binding changes. If a Namespace is unbound from a VirtualClusterPolicy (by removing the policy.k3k.io/policy-name label), K3k will clean up and remove the resources (such as ResourceQuotas, LimitRanges, and managed Namespace labels) that were originally applied by that policy. Similarly, if the label is changed to bind the Namespace to a new VirtualClusterPolicy, K3k will first remove the resources associated with the old policy before applying the configurations from the new one, ensuring a clean transition.

### Default Policy Values

If you create a `VirtualClusterPolicy` without specifying any `spec` fields (e.g., using `k3kcli policy create my-default-policy`), it will be created with default settings. Currently, this includes `spec.allowedMode` being set to `"shared"`.

```yaml
# Example of a minimal VCP (after creation with defaults)
apiVersion: k3k.io/v1beta1
kind: VirtualClusterPolicy
metadata:
  name: my-default-policy
spec:
  allowedMode: shared
```

## Key Capabilities & Examples

A `VirtualClusterPolicy` can configure several aspects of the Namespaces it's bound to and the virtual clusters operating within them.

### 1. Restricting Allowed Virtual Cluster Modes (`AllowedMode`)

You can restrict the `mode` ("shared", "virtual" or "hcp") in which K3k `Cluster` resources can be provisioned within bound Namespaces. A policy allows exactly one mode. If a `Cluster` is created in a bound Namespace with a different mode, it stays `Pending` with a `ValidationFailed` reason in its `Ready` condition, and nothing is provisioned.

**Example:** Allow only "shared" mode clusters.

```yaml
apiVersion: k3k.io/v1beta1
kind: VirtualClusterPolicy
metadata:
  name: shared-only-policy
spec:
  allowedMode: shared
```

You can also specify this using the CLI: `k3kcli policy create --mode shared shared-only-policy` (or `--mode virtual`, `--mode hcp`).

### 2. Defining Resource Quotas (`quota`)

You can define resource consumption limits for bound Namespaces by specifying a `ResourceQuota`. K3k will create a `ResourceQuota` object in each bound Namespace with the provided specifications.

**Example:** Set CPU, memory, and pod limits.

```yaml
apiVersion: k3k.io/v1beta1
kind: VirtualClusterPolicy
metadata:
  name: quota-policy
spec:
  quota:
    hard:
      cpu: "10"
      memory: "20Gi"
      pods: "10"
```

### 3. Setting Limit Ranges (`limit`)

You can define default resource requests/limits and min/max constraints for containers running in bound Namespaces by specifying a `LimitRange`. K3k will create a `LimitRange` object in each bound Namespace.

**Example:** Define default CPU requests/limits and min/max CPU.

```yaml
apiVersion: k3k.io/v1beta1
kind: VirtualClusterPolicy
metadata:
  name: limit-policy
spec:
  limit:
    limits:
    - default:
        cpu: "500m"
      defaultRequest:
        cpu: "500m"
      max:
        cpu: "1"
      min:
        cpu: "100m"
      type: Container
```

### 4. Managing Network Isolation (`disableNetworkPolicy`)

By default, K3k creates a `NetworkPolicy` in bound Namespaces to provide network isolation for virtual clusters. It only selects the workload pods synced to the host in shared mode, so it has no effect in virtual and hcp mode. You can disable the creation of this default policy.

**Example:** Disable the default NetworkPolicy.

```yaml
apiVersion: k3k.io/v1beta1
kind: VirtualClusterPolicy
metadata:
  name: no-default-netpol-policy
spec:
  disableNetworkPolicy: true
```

### 5. Enforcing Pod Security Admission (`podSecurityAdmissionLevel`)

You can enforce Pod Security Standards (PSS) by specifying a Pod Security Admission (PSA) level. K3k will apply the corresponding PSA labels to each bound Namespace. The allowed values are `privileged`, `baseline`, `restricted`, and this will add labels like `pod-security.kubernetes.io/enforce: <level>` to the bound Namespace.

**Example:** Enforce the "baseline" PSS level.

```yaml
apiVersion: k3k.io/v1beta1
kind: VirtualClusterPolicy
metadata:
  name: baseline-psa-policy
spec:
  podSecurityAdmissionLevel: baseline
```

## Field Behavior by Cluster Mode

Not every field applies to every cluster mode. In shared mode the workloads of the virtual cluster run on the host as synced pods. In virtual mode K3k runs both server and agent pods on the host. In hcp (Hosted Control Plane) mode K3k runs only the server pods on the host, and users join their own nodes from outside the host cluster. A policy never affects the nodes joined to an hcp cluster, or the workloads running on them.

Since a policy allows only one mode, only one column applies to it.

| Field | shared | virtual | hcp |
|---|---|---|---|
| `quota` | Namespace | Namespace | Namespace |
| `limit` | Namespace | Namespace | Namespace |
| `defaultNodeSelector` | Server + agent pods, synced workloads | Server + agent pods | Server pods |
| `defaultPriorityClass` | Server + agent pods, synced workloads | Server + agent pods | Server pods |
| `defaultServerAffinity` | Server pods | Server pods | Server pods |
| `defaultAgentAffinity` | Agent pods | Agent pods | No effect |
| `allowedMode` | Cluster validation | Cluster validation | Cluster validation |
| `disableNetworkPolicy` | Synced workloads | No effect | No effect |
| `podSecurityAdmissionLevel` | Namespace | Namespace | Namespace |
| `sync.storageClasses` | Cluster | Cluster | Cluster |
| `sync` (other resources) | Synced workloads | No effect | No effect |
| `securityContext` | Server + agent pods | Server + agent pods | Server pods |
| `runtimeClassName` | Server + agent pods | Server + agent pods | Server pods |
| `hostUsers` | Server + agent pods | Server + agent pods | Server pods |
| `allowedHostSchedulers` | Synced workloads | No effect | No effect |

* **Namespace**: applies to every pod or object in the bound Namespace, including the ones K3k creates for the clusters.
* **Agent pods**: in shared mode this is the k3k-kubelet pod.
* **Cluster**: `sync.storageClasses` copies the host StorageClasses into the virtual cluster, in every mode.

### Precedence over the Cluster fields

Despite the `default` prefix, a policy field that is set replaces the matching field of the `Cluster`, it doesn't only fill it when empty. `defaultNodeSelector` and `defaultPriorityClass` count as set when they are not empty; `defaultServerAffinity`, `defaultAgentAffinity`, `securityContext`, `runtimeClassName` and `hostUsers` when they are present.

If the resulting priority class doesn't exist, the `Cluster` stays `Pending` with a `ValidationFailed` reason, and K3k doesn't touch its server and agent pods until the PriorityClass is created. This also applies to a running cluster whose PriorityClass gets deleted: the running pods are not affected, but changes to the cluster are not applied.

### Things to know

* **Changing a policy recreates pods.** Changing a field that applies to server or agent pods (see the table) recreates those pods in every cluster of the bound Namespaces. Binding or unbinding a policy can do the same. In hcp mode this restarts the control plane. In virtual mode the agent pods don't keep their data, so the restarted agents join again as nodes and their workloads are restarted.
* **The quota is shared by the whole Namespace.** All the clusters in a bound Namespace share the same `ResourceQuota`, and it also counts the Secrets, Services and PersistentVolumeClaims K3k creates for them. A tight count on these objects can block K3k from provisioning a cluster.
* **Compute quotas need resources on the server pods.** If the quota sets compute resources (for example `requests.cpu` or `limits.memory`), every pod in the Namespace must declare them, and the server pods don't declare any by default. Cover every compute resource named in the quota with `limit` defaults, or with the `serverLimit` of the `Cluster`. Otherwise the server pods are rejected, and the error only shows in the events of the server StatefulSet.
* **`defaultServerAffinity` replaces the default server anti-affinity.** Without it, K3k prefers to spread the server pods of a cluster across nodes. In hcp mode this matters, because each server should run on its own node. Add a pod anti-affinity to the policy affinity to keep that behavior.
* **Use `defaultNodeSelector` to pin hcp servers to some nodes.** In hcp mode the node selector is also used to pick the host node addresses configured in the servers, while an affinity is not.
* **Pod Security Admission.** In hcp mode the server pods don't run privileged, so `baseline` works for them. `restricted` is not tested. In virtual mode the server and agent pods run privileged, so they need `privileged`.

### Example: a policy for hcp clusters

```yaml
apiVersion: k3k.io/v1beta1
kind: VirtualClusterPolicy
metadata:
  name: hcp-policy
spec:
  allowedMode: hcp
  defaultNodeSelector:
    example.com/hcp-servers: "true"
  podSecurityAdmissionLevel: baseline
  disableNetworkPolicy: true
  quota:
    hard:
      requests.cpu: "4"
      requests.memory: "8Gi"
      limits.cpu: "8"
      limits.memory: "16Gi"
      services.nodeports: "3"
  limit:
    limits:
    - type: Container
      default:
        cpu: "2"
        memory: "4Gi"
      defaultRequest:
        cpu: "500m"
        memory: "1Gi"
```

## Further Reading

* For a complete reference of all `VirtualClusterPolicy` spec fields, see the [API Reference for VirtualClusterPolicy](./crds/crds.md#virtualclusterpolicy).
* To understand how VCPs fit into the overall K3k system, see the [Architecture](./architecture.md) document.
