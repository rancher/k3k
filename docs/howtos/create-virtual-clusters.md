# How to: Create a Virtual Cluster

This guide walks through the various ways to create and manage virtual clusters in K3K. We'll cover common use cases using both the **Custom Resource Definitions (CRDs)** and the **K3K CLI**, so you can choose the method that fits your workflow.

> 📘 For full reference:  
> - [CRD Reference Documentation](../crds/crd-docs.md)  
> - [CLI Reference Documentation](../cli/cli-docs.md)  
> - [Full example](../advanced-usage.md)

> [!NOTE]
> 🚧 Some features are currently only available via the CRD interface. CLI support may be added in the future.

---

## Use Case: Create and Expose a Basic Virtual Cluster

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-ingress
spec:
  tlsSANs:
    - my-cluster.example.com
  expose:
    ingress:
      ingressClassName: nginx
      annotations:
        nginx.ingress.kubernetes.io/ssl-passthrough: "true"
        nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
        nginx.ingress.kubernetes.io/ssl-redirect: "HTTPS"
```

This will create a virtual cluster in `shared` mode and expose it via an ingress with the specified hostname.

### CLI Method

*No CLI method available yet*

---

## Use Case: Create a Virtual Cluster with Persistent Storage (**Default**)

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-persistent
spec:
  persistence:
    type: dynamic
    storageClassName: local-path
    storageRequestSize: 30Gi
```

This ensures that the virtual cluster stores its state persistently with a 30Gi volume.  
If `storageClassName` is not set it will default to the default StorageClass.  
If `storageRequestSize` is not set it will request a 1Gi volume by default.  

### CLI Method

```sh
k3kcli cluster create \
  --persistence-type dynamic \
  --storage-class-name local-path \
  k3kcluster-persistent
```

> [!NOTE]
> The `k3kcli` does not support configuring the `storageRequestSize` yet.

---

## Use Case: Create a Highly Available Virtual Cluster in `shared` mode

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-ha
spec:
  servers: 3
```

This will create a virtual cluster with 3 servers and a default 1Gi volume for persistence.  

### CLI Method

```sh
k3kcli cluster create \
  --servers 3 \
  k3kcluster-ha
```

---

## Use Case: Create a Highly Available Virtual Cluster in `virtual` mode

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-virtual
spec:
  mode: virtual
  servers: 3
  agents: 3
```

This will create a virtual cluster with 3 servers and 3 agents and a default 1Gi volume for persistence.  
> [!NOTE]
> Agents only exist for `virtual` mode.

### CLI Method

```sh
k3kcli cluster create \
  --agents 3 \
  --servers 3 \
  --mode virtual \
  k3kcluster-virtual
```

---

## Use Case: Create an Ephemeral Virtual Cluster

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-ephemeral
spec:
  persistence:
    type: ephemeral
```

This will create an ephemeral virtual cluster with no persistence and a single server.  

### CLI Method

```sh
k3kcli cluster create \
  --persistence-type ephemeral \
  k3kcluster-ephemeral
```

---

## Use Case: Create a Virtual Cluster with a Custom Kubernetes Version

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-custom-k8s
spec:
  version: "v1.33.1-k3s1"
```

This sets the virtual cluster's Kubernetes version explicitly.  
> [!NOTE]
> Only [K3s](https://k3s.io) distributions are supported. You can find compatible versions on the K3s GitHub [release page](https://github.com/k3s-io/k3s/releases).

### CLI Method

```sh
k3kcli cluster create \
  --version v1.33.1-k3s1 \
  k3kcluster-custom-k8s
```

---

## Use Case: Create a Virtual Cluster with Custom Resource Limits

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-resourced
spec:
  mode: virtual
  serverLimit:
    cpu: "1"
    memory: "2Gi"
  workerLimit:
    cpu: "1"
    memory: "2Gi"
```

This configures the CPU and memory limit for the virtual cluster.

### CLI Method

*No CLI method available yet*

---

## Use Case: Create a Virtual Cluster on specific host nodes

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-node-placed
spec:
  nodeSelector:
    disktype: ssd
```

This places the virtual cluster on nodes with the label `disktype: ssd`.  
> [!NOTE]
> In `shared` mode workloads are also scheduled on the selected nodes

### CLI Method

*No CLI method available yet*

---

## Use Case: Create a Virtual Cluster with a Rancher Host Cluster Kubeconfig

When using a `kubeconfig` generated with Rancher, you need to specify with the CLI the desired host for the virtual cluster `kubeconfig`.  
By default, `k3kcli` uses the current host `kubeconfig` to determine the target cluster.

### CRD Method

*Not applicable*

### CLI Method

```sh
k3kcli cluster create \
  --kubeconfig-server https://abc.xyz \
  k3kcluster-host-rancher
```

---

## Use Case: Create a Virtual Cluster Behind an HTTP Proxy

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-http-proxy
spec:
  serverEnvs:
    - name: HTTP_PROXY
      value: "http://abc.xyz"
  agentEnvs:
    - name: HTTP_PROXY
      value: "http://abc.xyz"
```

This configures an HTTP proxy for both servers and agents in the virtual cluster.  
> [!NOTE]  
> This can be leveraged to pass **any custom environment variables** to the servers and agents — not just proxy settings.

### CLI Method

```sh
k3kcli cluster create  \
  --server-envs HTTP_PROXY=http://abc.xyz \
  --agent-envs HTTP_PROXY=http://abc.xyz \
  k3kcluster-http-proxy
```

---

## How to: Connect to a Virtual Cluster

Once the virtual cluster is running, you can connect to it using the CLI:

### CLI Method

```sh
k3kcli kubeconfig generate --namespace k3k-mycluster --name mycluster 
export KUBECONFIG=$PWD/mycluster-kubeconfig.yaml
kubectl get nodes
```

This command generates a `kubeconfig` file, which you can use to access your virtual cluster via `kubectl`.
