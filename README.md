# K3k: Kubernetes in Kubernetes

[![Experimental](https://img.shields.io/badge/status-experimental-orange.svg)](https://shields.io/)
[![Go Report Card](https://goreportcard.com/badge/github.com/rancher/k3k)](https://goreportcard.com/report/github.com/rancher/k3k)
![Tests](https://github.com/rancher/k3k/actions/workflows/test.yaml/badge.svg)
![Build](https://github.com/rancher/k3k/actions/workflows/build.yml/badge.svg)


K3k, Kubernetes in Kubernetes, is a tool that empowers you to create and manage isolated K3s clusters within your existing Kubernetes environment.  It enables efficient multi-tenancy, streamlined experimentation, and robust resource isolation, minimizing infrastructure costs by allowing you to run multiple lightweight Kubernetes clusters on the same physical host. K3k offers both "shared" mode, optimizing resource utilization, and "virtual" mode, providing complete isolation with dedicated K3s server pods. This allows you to access a full Kubernetes experience without the overhead of managing separate physical resources. 

K3k integrates seamlessly with Rancher for simplified management of your embedded clusters.


**Experimental Tool**

This project is still under development and is considered experimental. It may have limitations, bugs, or changes. Please use with caution and report any issues you encounter. We appreciate your feedback as we continue to refine and improve this tool.


## Features and Benefits

- **Resource Isolation:** Ensure workload isolation and prevent resource contention between teams or applications. K3k allows you to define resource limits and quotas for each embedded cluster, guaranteeing that one team's workloads won't impact another's performance.

- **Simplified Multi-Tenancy:** Easily create dedicated Kubernetes environments for different users or projects, simplifying access control and management. Provide each team with their own isolated cluster, complete with its own namespaces, RBAC, and resource quotas, without the complexity of managing multiple physical clusters.

- **Lightweight and Fast:** Leverage the lightweight nature of K3s to spin up and tear down clusters quickly, accelerating development and testing cycles. Spin up a new K3k cluster in seconds, test your application in a clean environment, and tear it down just as quickly, streamlining your CI/CD pipeline.

- **Optimized Resource Utilization (Shared Mode):** Maximize your infrastructure investment by running multiple K3s clusters on the same physical host. K3k's shared mode allows you to efficiently share underlying resources, reducing overhead and minimizing costs.

- **Complete Isolation (Virtual Mode):** For enhanced security and isolation, K3k's virtual mode provides dedicated K3s server pods for each embedded cluster. This ensures complete separation of workloads and eliminates any potential resource contention or security risks.

- **Rancher Integration:** Simplify the management of your K3k clusters with Rancher. Leverage Rancher's intuitive UI and powerful features to monitor, manage, and scale your embedded clusters with ease.


## Installation

This section provides instructions on how to install K3k and the `k3kcli`.


### Prerequisites

* [Helm](https://helm.sh) must be installed to use the charts. Please refer to Helm's [documentation](https://helm.sh/docs) to get started.
* An existing [RKE2](https://docs.rke2.io/install/quickstart) Kubernetes cluster (recommended).
* A configured storage provider with a default storage class.

**Note:** If you do not have a storage provider, you can configure the cluster to use ephemeral or static storage. Please consult the [k3kcli advance usage](./docs/advanced-usage.md#using-the-cli) for instructions on using these options.

### Install the K3k controller

1. Add the K3k Helm repository:

   ```bash
   helm repo add k3k https://rancher.github.io/k3k
   helm repo update
   ```

2. Install the K3k controller:

   ```bash
   helm install --namespace k3k-system --create-namespace k3k k3k/k3k --devel
   ```

**NOTE:** K3k is currently under development, so the chart is marked as a development chart. This means you need to add the `--devel` flag to install it.  For production use, keep an eye on releases for stable versions. We recommend using the latest released version when possible.


### Install the `k3kcli`

The `k3kcli` provides a quick and easy way to create K3k clusters and automatically exposes them via a kubeconfig.

To install it, simply download the latest available version for your architecture from the GitHub Releases page.

For example, you can download the Linux amd64 version with:

```
wget -qO k3kcli https://github.com/rancher/k3k/releases/download/v0.3.0/k3kcli-linux-amd64 && \
  chmod +x k3kcli && \
  sudo mv k3kcli /usr/local/bin
```

You should now be able to run:
```bash
-> % k3kcli --version
k3kcli Version: v0.3.0
```


## Usage

This section provides examples of how to use the `k3kcli` to manage your K3k clusters.

**K3k operates within the context of your currently configured `kubectl` context.** This means that K3k respects the standard Kubernetes mechanisms for context configuration, including the `--kubeconfig` flag, the `$KUBECONFIG` environment variable, and the default `$HOME/.kube/config` file. Any K3k clusters you create will reside within the Kubernetes cluster that your `kubectl` is currently pointing to.


### Creating a K3k Cluster

To create a new K3k cluster, use the following command:

```bash
k3kcli cluster create mycluster
```
> [!NOTE]
> **Creating a K3k Cluster on a Rancher-Managed Host Cluster**
>
> If your *host* Kubernetes cluster is managed by Rancher (e.g., your kubeconfig's `server` address includes a Rancher URL), use the `--kubeconfig-server` flag when creating your K3k cluster:
>
>```bash
>k3kcli cluster create --kubeconfig-server <host_node_IP_or_load_balancer_IP> mycluster
>```
>
> This ensures the generated kubeconfig connects to the correct endpoint.

When the K3s server is ready, `k3kcli` will generate the necessary kubeconfig file and print instructions on how to use it.  

Here's an example of the output:

```bash
INFO[0000] Creating a new cluster [mycluster]          
INFO[0000] Extracting Kubeconfig for [mycluster] cluster 
INFO[0000] waiting for cluster to be available..        
INFO[0073] certificate CN=system:admin,O=system:masters signed by CN=k3s-client-ca@1738746570: notBefore=2025-02-05 09:09:30 +0000 UTC notAfter=2026-02-05 09:10:42 +0000 UTC 
INFO[0073] You can start using the cluster with: 

        export KUBECONFIG=/my/current/directory/mycluster-kubeconfig.yaml
        kubectl cluster-info  
```

After exporting the generated kubeconfig, you should be able to reach your Kubernetes cluster:

```bash
export KUBECONFIG=/my/current/directory/mycluster-kubeconfig.yaml
kubectl get nodes
kubectl get pods -A
```

You can also directly create a Cluster resource in some namespace, to create a K3k cluster:

```bash
kubectl apply -f - <<EOF
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: mycluster
  namespace: k3k-mycluster
EOF
```

and use the `k3kcli` to retrieve the kubeconfig:

```bash
k3kcli kubeconfig generate --namespace k3k-mycluster --name mycluster 
```


### Deleting a K3k Cluster

To delete a K3k cluster, use the following command:

```bash
k3kcli cluster delete mycluster
```


## Architecture

For a detailed explanation of the K3k architecture, please refer to the [Architecture documentation](./docs/architecture.md).


## Advanced Usage

For more in-depth examples and information on advanced K3k usage, including details on shared vs. virtual modes, resource management, and other configuration options, please see the [Advanced Usage documentation](./docs/advanced-usage.md).


## Development

If you're interested in building K3k from source or contributing to the project, please refer to the [Development documentation](./docs/development.md).


## License

Copyright (c) 2014-2025 [SUSE](http://rancher.com/)

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the License. You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.
