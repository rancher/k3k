## Virtual Kubelet

This package provides an impelementation of a virtual cluster node using [virtual-kubelet](https://github.com/virtual-kubelet/virtual-kubelet).

The implementation is based on several projects, including:
- [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet)
- [Kubectl](https://github.com/kubernetes/kubectl)
- [Client-go](https://github.com/kubernetes/client-go)
- [Azure-Aci](https://github.com/virtual-kubelet/azure-aci)

## Overview

This project creates a node that registers itself in the virtual cluster. When workloads are scheduled to this node, it simply creates/updates the workload on the host cluster.  

## Usage

Build/Push the image using (from the root of rancher/k3k):

```
make build
docker buildx build -f package/Dockerfile . -t $REPO/$IMAGE:$TAG
```

When running, it is recommended to deploy a k3k cluster with 1 server (with `--disable-agent` as a server arg) and no agents (so that the workloads can only be scheduled on the virtual node/host cluster).

After the image is built, it should be deployed with the following ENV vars set:
- `CLUSTER_NAME` should be the name of the cluster.
- `CLUSTER_NAMESPACE` should be the namespace the cluster is running in.
- `HOST_KUBECONFIG` should be the path on the local filesystem (in container) to a kubeconfig for the host cluster (likely stored in a secret/mounted as a volume).
- `VIRT_KUBECONFIG`should be the path on the local filesystem (in container) to a kubeconfig for the virtual cluster (likely stored in a secret/mounted as a volume).
- `VIRT_POD_IP` should be the IP that the container is accessible from.

This project is still under development and there are many features yet to be implemented, but it can run a basic nginx pod.

