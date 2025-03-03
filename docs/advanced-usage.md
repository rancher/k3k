# Advanced Usage

This document provides advanced usage information for k3k, including detailed use cases and explanations of the `Cluster` resource fields for customization.

## Customizing the Cluster Resource

The `Cluster` resource provides a variety of fields for customizing the behavior of your virtual clusters. You can check the [CRD documentation](./crds/crd-docs.md) for the full specs.

**Note:** Most of these customization options can also be configured using the `k3kcli` tool. Refer to the [k3kcli](./cli/cli-docs.md) documentation for more details.



This example creates a "shared" mode K3k cluster with:

- 3 servers
- K3s version v1.31.3-k3s1
- Custom network configuration 
- Deployment on specific nodes with the `nodeSelector`
- `kube-api` exposed using an ingress
- Custom K3s `serverArgs`
- ETCD data persisted using a `PVC`


```yaml
apiVersion: k3k.io/v1alpha1
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
        nginx.ingress.kubernetes.io/backend-protocol: "true"
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

In this example we are exposing the Cluster with a Nginx ingress-controller, that has to be configured with the `--enable-ssl-passthrough` flag.


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

You can check the [k3kcli documentation](./cli/cli-docs.md) for the full specs.

### No storage provider:

* Ephemeral Storage:

    ```bash
    k3kcli cluster create my-cluster --persistence-type ephemeral
    ```

*Important Notes:*

* Using `--persistence-type ephemeral` will result in data loss if the nodes are restarted.

* It is highly recommended to use `--persistence-type dynamic` with a configured storage class.