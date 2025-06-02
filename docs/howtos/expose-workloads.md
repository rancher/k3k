# How-to: Expose Workloads Outside the Virtual Cluster

This guide explains how to expose workloads running in k3k-managed virtual clusters to external networks. Behavior varies depending on the operating mode of the virtual cluster.

## Virtual Mode

> [!CAUTION]
> **Not Supported**  
> In *virtual mode*, direct external exposure of workloads is **not available**.  
> This mode is designed for strong isolation and does not expose the virtual cluster's network directly.

## Shared Mode

In *shared mode*, workloads can be exposed to the external network using standard Kubernetes service types or an ingress controller, depending on your requirements.

> [!NOTE]
> *`Services`* are always synced from the virtual cluster to the host cluster following the same principle described [here](../architecture.md#shared-mode) for pods. 

### Option 1: Use `NodePort` or `LoadBalancer`

To expose a service such as a web application outside the host cluster:

- **`NodePort`**:  
  Exposes the service on a static port on each node’s IP.  
  Access the service at `http://<NodeIP>:<NodePort>`.

- **`LoadBalancer`**:  
  Provisions an external load balancer (if supported by the environment) and exposes the service via the load balancer’s IP.

> **Note**  
> The `LoadBalancer` IP is currently not reflected back to the virtual cluster service.  
> [k3k issue #365](https://github.com/rancher/k3k/issues/365)

### Option 2: Use `ClusterIP` for Internal Communication

If the workload should only be accessible to other services or pods *within* the host cluster:

- Use the `ClusterIP` service type.  
  This exposes the service on an internal IP, only reachable inside the host cluster.

### Option 3: Use Ingress for HTTP/HTTPS Routing

For more advanced routing (e.g., hostname- or path-based routing), deploy an **Ingress controller** in the virtual cluster, and expose it via `NodePort` or `LoadBalancer`.

This allows you to:

- Define Ingress resources in the virtual cluster.
- Route external traffic to services within the virtual cluster.

>**Note**  
> Support for using the host cluster's Ingress controller from a virtual cluster is being tracked in  
> [k3k issue #356](https://github.com/rancher/k3k/issues/356)
