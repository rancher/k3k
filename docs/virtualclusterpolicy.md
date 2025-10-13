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

You can restrict the `mode` (e.g., "shared" or "virtual") in which K3k `Cluster` resources can be provisioned within bound Namespaces. If a `Cluster` is created in a bound Namespace with a mode not allowed in `allowedMode`, its creation might proceed but an error should be reported in the `Cluster` resource's status.

**Example:** Allow only "shared" mode clusters.

```yaml
apiVersion: k3k.io/v1beta1
kind: VirtualClusterPolicy
metadata:
  name: shared-only-policy
spec:
  allowedModeTypes:
  - shared
```

You can also specify this using the CLI: `k3kcli policy create --mode shared shared-only-policy` (or `--mode virtual`).

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

By default, K3k creates a `NetworkPolicy` in bound Namespaces to provide network isolation for virtual clusters (especially in shared mode). You can disable the creation of this default policy.

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

## Further Reading

* For a complete reference of all `VirtualClusterPolicy` spec fields, see the [API Reference for VirtualClusterPolicy](./crds/crd-docs.md#virtualclusterpolicy).
* To understand how VCPs fit into the overall K3k system, see the [Architecture](./architecture.md) document.
