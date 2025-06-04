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
  mode: shared
  tlsSANs:
    - my-cluster.example.com
  expose:
    ingress:
      ingressClassName: nginx
      annotations:
        nginx.ingress.kubernetes.io/ssl-passthrough: "true"
        nginx.ingress.kubernetes.io/backend-protocol: "true"
        nginx.ingress.kubernetes.io/ssl-redirect: "HTTPS"
```

This will create a virtual cluster and expose it via an ingress with the specified hostname.

### CLI Method

*No CLI method available yet*

---

## Use Case: Create a Virtual Cluster with Persistent Storage

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
If `storageRequestSize` is not set it will request a 1Gi volume.  

### CLI Method

```sh
k3kcli cluster create \
  --persistence-type dynamic \
  --storage-class-name local-path \
  --storage-request-size 30Gi \
  k3kcluster-persistent
```

---

## Use Case: Create a Virtual Cluster in `virtual` mode

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-virtual
spec:
  mode: virtual
  agents: 1
```

This will create a virtual cluster in `virtual` mode.

### CLI Method

```sh
k3kcli cluster create \
  --agents 1 \
  --mode virtual \
  k3kcluster-virtual
```

---

## Use Case: Create a Highly Available Virtual Cluster

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-ha
spec:
  servers: 3
  persistence:
    type: dynamic
```

This will create a virtual cluster with 3 servers and persistence.  
For `virtual` mode, make sure to set `agents` to `3` or more.

### CLI Method

```sh
k3kcli cluster create \
  --servers 3 \
  --persistence-type dynamic \
  k3kcluster-ha
```

---

## Use Case: Create an ephemeral virtual cluster

### CRD Method

```yaml
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: k3kcluster-ephemeral
spec:
  servers: 1
  persistence:
    type: ephemeral
```

This will create an ephemeral virtual cluster with no persistence.  

### CLI Method

```sh
k3kcli cluster create \
  --servers 1 \
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

## Use Case: Create a Virtual Cluster with Node Selector

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

### CLI Method

*No CLI method available yet*

---

## Use Case: Create a Virtual Cluster with a Rancher Host Cluster Kubeconfig

When using a `kubeconfig` generated with Rancher, you need to specify with the CLI the desired host for the virtual cluster `kubeconfig`.  
The default value, when using the `k3kcli`, is the one in the host `kubeconfig`.

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
    HTTP_PROXY: http://abc.xyz
  agentEnvs:
    HTTP_PROXY: http://abc.xyz
```

This configures an HTTP proxy for all nodes in the virtual cluster.

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

This command retrieves a `kubeconfig` to access your virtual cluster.
