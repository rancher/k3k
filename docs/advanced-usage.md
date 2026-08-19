# Advanced Usage

This document provides advanced usage information for k3k, including detailed use cases and explanations of the `Cluster` resource fields for customization.

## Customizing the Cluster Resource

The `Cluster` resource provides a variety of fields for customizing the behavior of your virtual clusters. You can check the [CRD documentation](./crds/crds.md) for the full specs.

**Note:** Most of these customization options can also be configured using the `k3kcli` tool. Refer to the [k3kcli](./cli/k3kcli.md) documentation for more details.



This example creates a "shared" mode K3k cluster with:

- 3 servers
- K3s version v1.31.3-k3s1
- Custom network configuration 
- Deployment on specific nodes with the `nodeSelector`
- `kube-api` exposed using an ingress
- Custom K3s `serverArgs`
- ETCD data persisted using a `PVC`


```yaml
apiVersion: k3k.io/v1beta1
kind: Cluster
metadata:
  name: my-virtual-cluster
  namespace: my-namespace
spec:
  mode: shared
  version: v1.31.3-k3s1
  servers: 3
  tlsSANs:
    - my-cluster.example.com
  nodeSelector:
    disktype: ssd
  expose:
    ingress:
      ingressClassName: nginx
      annotations:
        nginx.ingress.kubernetes.io/ssl-passthrough: "true"
        nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
        nginx.ingress.kubernetes.io/ssl-redirect: "HTTPS"
  clusterCIDR: 10.42.0.0/16
  serviceCIDR: 10.43.0.0/16
  clusterDNS: 10.43.0.10
  serverArgs:
  - --tls-san=my-cluster.example.com
  persistence:
    type: dynamic
    storageClassName: local-path
```


### `mode`

The `mode` field specifies the cluster provisioning mode, which can be either `shared` or `virtual`. The default mode is `shared`.

* **`shared` mode:** In this mode, the virtual cluster shares the host cluster's resources and networking. This mode is suitable for lightweight workloads and development environments where isolation is not a primary concern.
* **`virtual` mode:** In this mode, the virtual cluster runs as a separate K3s cluster within the host cluster. This mode provides stronger isolation and is suitable for production workloads or when dedicated resources are required.


### `version`

The `version` field specifies the Kubernetes version to be used by the virtual nodes. If not specified, K3k will use the same K3s version as the host cluster. For example, if the host cluster is running Kubernetes v1.31.3, K3k will use the corresponding K3s version (e.g., `v1.31.3-k3s1`).


### `servers`

The `servers` field specifies the number of K3s server nodes to deploy for the virtual cluster. The default value is 1.


### `agents`

The `agents` field specifies the number of K3s agent nodes to deploy for the virtual cluster. The default value is 0.

**Note:** In `shared` mode, this field is ignored, as the Virtual Kubelet acts as the agent, and there are no K3s worker nodes.


### `nodeSelector`

The `nodeSelector` field allows you to specify a node selector that will be applied to all server/agent pods. In `shared` mode, the node selector will also be applied to the workloads.


### `expose`

The `expose` field contains options for exposing the API server of the virtual cluster. By default, the API server is only exposed as a `ClusterIP`, which is relatively secure but difficult to access from outside the cluster.

You can use the `expose` field to enable exposure via `NodePort`, `LoadBalancer`, or `Ingress`.

#### TLS passthrough is required

The K3s API server authenticates clients with mTLS certificates, so **the TLS connection must reach
the API server untouched**. An ingress controller that terminates TLS will break both `kubectl` and
agent authentication. Any controller used with `expose.ingress` must therefore support TLS
passthrough.

`expose.ingress` also requires at least one **DNS name** in `spec.tlsSANs`: the SANs are used as the
Ingress hosts, and the Ingress API does not accept IP addresses there. IPs in `tlsSANs` are ignored
when building the Ingress, and a cluster with no DNS SAN stays in the `Pending` phase with a
`ValidationFailed` condition rather than generating an invalid Ingress.

#### Nginx

Supported directly through `expose.ingress`. The ingress controller has to be started with the
`--enable-ssl-passthrough` flag, and the annotations enabling passthrough must be set on the
Ingress:

```yaml
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

#### Traefik

Traefik **cannot** perform layer 4 TLS passthrough with a standard `Ingress`, so `expose.ingress`
does not work with it. This matters on K3s and RKE2, where Traefik is the default ingress
controller.

Use `expose.loadBalancer` or `expose.nodePort` instead, or leave `expose` unset and create a Traefik
`IngressRouteTCP` pointing at the cluster's `ClusterIP` service:

```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRouteTCP
metadata:
  name: my-virtual-cluster
  namespace: my-namespace
spec:
  entryPoints:
    - websecure
  routes:
    - match: HostSNI(`my-cluster.example.com`)
      services:
        - name: k3k-my-virtual-cluster-service # k3k-<cluster-name>-service
          port: 443
  tls:
    passthrough: true
```

Add `my-cluster.example.com` to `spec.tlsSANs` so the API server certificate covers it, and generate
the kubeconfig with the matching endpoint:

```bash
k3kcli kubeconfig generate my-namespace/my-virtual-cluster \
  --kubeconfig-server https://my-cluster.example.com
```

**Limitation in `hcp` mode:** when the routing resource is managed outside of K3k, K3k does not know
the external endpoint. In `hcp` mode it owns the `default/kubernetes` Endpoints inside the virtual
cluster so that pods on external worker nodes can reach the API server, and without an `expose`
configuration it can only point them at the host cluster's `ClusterIP`, which external nodes cannot
route to. For `hcp` clusters with external workers, use `expose.nodePort` or `expose.loadBalancer`.


### `clusterCIDR`

The `clusterCIDR` field specifies the CIDR range for the pods of the cluster. The default value is `10.42.0.0/16` in shared mode, and `10.52.0.0/16` in virtual mode.


### `serviceCIDR`

The `serviceCIDR` field specifies the CIDR range for the services in the cluster. The default value is `10.43.0.0/16` in shared mode, and `10.53.0.0/16` in virtual mode.

**Note:** In `shared` mode, the `serviceCIDR` should match the host cluster's `serviceCIDR` to prevent conflicts and in `virtual` mode both `serviceCIDR` and `clusterCIDR` should be different than the host cluster.


### `clusterDNS`

The `clusterDNS` field specifies the IP address for the CoreDNS service. It needs to be in the range provided by `serviceCIDR`. The default value is `10.43.0.10`.


### `serverArgs`

The `serverArgs` field allows you to specify additional arguments to be passed to the K3s server pods.

## Using the cli

You can check the [k3kcli documentation](./cli/k3kcli.md) for the full specs.

### No storage provider:

* Ephemeral Storage:

    ```bash
    k3kcli cluster create --persistence-type ephemeral my-cluster
    ```

*Important Notes:*

* Using `--persistence-type ephemeral` will result in data loss if the nodes are restarted.

* It is highly recommended to use `--persistence-type dynamic` with a configured storage class.
