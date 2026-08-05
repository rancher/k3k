# Using Kata Containers

> **Experimental:** Kata Containers support is in early development. Expect rough edges.

Kata Containers runs each pod inside a lightweight QEMU VM, providing stronger isolation than runc. When used with k3k, this means each virtual cluster's server and agent pods run as hardware-virtualized guests on the host node.

This guide covers the full setup: installing Kata, configuring the QEMU runtime, optional erofs snapshotter, and deploying a cluster.

# Prerequisites

## 1. Requirements

- KVM available on the host node (`/dev/kvm` accessible)
- `vhost_net` and `vhost_vsock` kernel modules available
- K3s or RKE2 installed on the host

Ensure the required kernel modules are availible:

```bash
sudo modprobe vhost_net
sudo modprobe vhost_vsock
```

The shim should handle the loading of these, but to be explicit, add them to `/etc/modules-load.d/kata.conf`.

The use of the erofs snapshotter is optional but recommended for performance. To confirm the host meets the requirements, you may need the erofs-utils package.

```bash
sudo modprobe erofs
mkfs.erofs --version
```

# Helm Installation (Recommended)

Using Helm is the quickest and simplest way to install Kata Containers. It automates the distribution of Kata binaries, configures containerd, and registers the necessary RuntimeClasses across your cluster. It has been tested with RKE2 1.35 and Kata 4.0.0.

Ensure you are using a v3 containerd config. This will be the default on 1.35 if no template is provided, or you can create a blank v3 template at `/var/lib/rancher/rke2/agent/etc/containerd/config-v3.toml.tmpl`:

```toml
{{ template "base" . }}
```

Install the `kata-deploy` Helm chart:

```bash
helm upgrade --install kata-deploy oci://ghcr.io/kata-containers/kata-deploy-charts/kata-deploy \
  --version 4.0.0 \
  --namespace kata-deploy \
  --create-namespace \
  --values=kata-values.yaml
```

Contents of `kata-values.yaml`:

```yaml
k8sDistribution: "rke2"

containerd:
  userDropIn: |
    version = 3

    [plugins.'io.containerd.snapshotter.v1.erofs']
      enable_fsverity = false


snapshotter:
  setup: ["erofs"] # omit of not using erofs

shims:
  disableAll: true

  qemu-runtime-rs:
    enabled: true
    supportedArches:
      - amd64
    allowedHypervisorAnnotations: []
    containerd:
      snapshotter: "erofs"  # omit of not using erofs
    dropIn: |
      [hypervisor.qemu]
      disable_block_device_use = false

      [runtime]
      emptydir_mode = "block-plain"

debug: true

defaultShim:
  amd64: qemu-runtime-rs
```

# Create a Cluster

Kata-based clusters require `mode: virtual` and `persistence.type: ephemeral`:

- **Virtual mode** is required because Kata needs a full K3s agent with its own container runtime running on the host. Shared mode uses a virtual kubelet that bypasses node-level runtimes entirely.
- **Ephemeral persistence** is required because dynamic persistence creates a PVC that conflicts with how Kata manages block device storage inside the guest VM.

The example below uses an external PostgreSQL datastore via `secretMounts` to provide persistence.

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: test-k3k
---
apiVersion: v1
kind: Secret
metadata:
  name: datastore-config
  namespace: test-k3k
type: Opaque
stringData:
  config.yaml: |
    datastore-endpoint: postgres://username:password@host:5432/dbname
    cluster-init: false
    server: ""
---
apiVersion: k3k.io/v1beta1
kind: Cluster
metadata:
  name: test
  namespace: test-k3k
spec:
  mode: virtual
  runtimeClassName: kata-qemu-runtime-rs
  persistence:
    type: ephemeral
  servers: 3
  secretMounts:
    - name: externaldb-init-config
      secretName: datastore-config
      mountPath: /opt/rancher/k3s/init/config.yaml.d/
      role: server
    - name: externaldb-server-config
      secretName: datastore-config
      mountPath: /opt/rancher/k3s/server/config.yaml.d/
```

# Notes

- Currently `runtime-rs` is not supported when SELinux is enabled.
