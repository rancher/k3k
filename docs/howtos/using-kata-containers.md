# Using Kata Containers

> **Experimental:** Kata Containers support is in early development. Expect rough edges.

Kata Containers runs each pod inside a lightweight QEMU VM, providing stronger isolation than runc. When used with k3k, this means each virtual cluster's server and agent pods run as hardware-virtualized guests on the host node.

This guide covers the full setup: installing Kata, configuring the QEMU runtime and devmapper snapshotter, preparing a custom K3s image, and deploying a cluster.

## Requirements

- KVM available on the host node (`/dev/kvm` accessible)
- `vhost_net` and `vhost_vsock` kernel modules available
- `dmsetup` and `losetup` available (device-mapper utilities)
- K3s or RKE2 installed on the host

## 1. Install Kata Containers

Download and extract the Kata static binary bundle, then symlink the containerd shim:

```bash
curl -sSL https://github.com/kata-containers/kata-containers/releases/download/3.31.0/kata-static-3.31.0-amd64.tar.zst \
  | sudo tar --zstd -xvf - -C /

ln -s /opt/kata/runtime-rs/bin/containerd-shim-kata-v2 /usr/local/bin/containerd-shim-kata-v2
```

## 2. Configure the QEMU Runtime

The default Kata QEMU config requires a few changes. The key ones switch the rootfs and block device transports from `virtio-pmem`/`virtio-scsi` to `virtio-blk-pci`, which is necessary because the devmapper snapshotter provides block devices rather than overlay filesystems. `shared_fs` is also disabled since `virtio-fs` is not needed when using block-backed storage.

```bash
sed -i \
  -e 's/vm_rootfs_driver = "virtio-pmem"/vm_rootfs_driver = "virtio-blk-pci"/' \
  -e 's/shared_fs = "virtio-fs"/shared_fs = "none"/' \
  -e 's/block_device_driver = "virtio-scsi"/block_device_driver = "virtio-blk-pci"/' \
  -e 's/disable_image_nvdimm = false/disable_image_nvdimm = true/' \
  /opt/kata/share/defaults/kata-containers/runtime-rs/configuration-qemu-runtime-rs.toml
```

## 3. Set Up the devmapper Snapshotter

The default snapshotter will use virtio-fs as the container filesystem, the default overlayfs snapshotter does not work with this type, so devmapper is required in order to provide a block device.

The script below creates a thin-pool backed by loopback devices. This is adequate for development and testing. The [device-mapper thin provisioning documentation](https://www.kernel.org/doc/html/latest/admin-guide/device-mapper/thin-provisioning.html) covers the full lifecycle, which is more involved than the example here. `erofs` may be a suitable replacement in future to eliminate the need to manage this pool entirely.

```bash
#!/bin/bash

DIR=/opt/devmapper
CONTAINERD_SOCK="/run/k3s/containerd/containerd.sock"

set -euxo pipefail

name="k3s"

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root."
  exit 1
fi

create_loopback_device() {
    local path=$1
    local size=$2

    if [[ ! -f "$path" ]]; then
        touch "$path"
        truncate -s "$size" "$path"
    fi

    local dev=$(losetup --output NAME --noheadings --associated "$path")
    if [[ -z "$dev" ]]; then
        dev=$(losetup --find --show $path)
    fi
    echo $dev
}

pool_create() {
    mkdir -p $DIR

    local datadev=$(create_loopback_device "$DIR/data" '10G')
    local metadev=$(create_loopback_device "$DIR/metadata" '1G')

    if dmsetup info "$name" &>/dev/null; then
        echo "Thin pool '$name' already exists, ensuring it is active."
        dmsetup resume "$name" 2>/dev/null || true
        return
    fi

    local sectorsize=512
    local datasize="$(blockdev --getsize64 -q ${datadev})"
    local length_sectors=$(bc <<< "${datasize}/${sectorsize}")
    local thinp_table="0 ${length_sectors} thin-pool ${metadev} ${datadev} 128 32768 1 skip_block_zeroing"
    dmsetup create "$name" --table "${thinp_table}"
}

pool_create
```

The pool does not survive a reboot. Consider wrapping `pool_create` in a systemd service that runs before `k3s.service`.

## 4. Configure K3s containerd

Place the following template at `/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/custom.toml`. It registers the `kata-qemu` runtime handler and configures the devmapper snapshotter.

```toml
version = 3

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.kata-qemu]
  runtime_type = "io.containerd.kata.v2"
  snapshotter = "devmapper"
  privileged_without_host_devices = true
  pod_annotations = ["io.katacontainers.*"]

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.kata-qemu.options]
  ConfigPath = "/opt/kata/share/defaults/kata-containers/runtime-rs/configuration-qemu-runtime-rs.toml"

[plugins.'io.containerd.snapshotter.v1.devmapper']
  pool_name = "k3s"
  root_path = "/opt/devmapper"
  base_image_size = "4GB"
  discard_blocks = true
```
## 5. Fixing emptyDir mounts

The standard K3s image declares `emptyDir` volumes in its image manifest. Kata detects these as tmpfs mounts and fails to start the container. The k3k controller automatically strips `emptyDir` volumes from the pod spec at runtime when the cluster's `runtimeClassName` starts with `kata`, but the declarations baked into the image manifest will still be in place by default.

We can avoid emptyDir mounts from image manifests by either of the following options:

### 5.1 Ignore the image manifest volume declaration (recommended)

Configuring containerd to ignore the image manifest volumes, this is a simpler option but is a global setting and will apply to all runtimes on a particular node.

Place the below at `/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/volumes.toml`
```toml
version = 3

[plugins.'io.containerd.cri.v1.runtime']
  ignore_image_defined_volumes = true
```

### 5.2 Build a custom K3S image

Build a custom image that omits those declarations:

```dockerfile
FROM rancher/k3s:v1.35.3-k3s1 AS rancher

FROM scratch

COPY --from=rancher / /

ENV PATH=/var/lib/rancher/k3s/data/cni:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/bin/aux
ENV CRI_CONFIG_FILE=/var/lib/rancher/k3s/agent/etc/crictl.yaml
```

Push this image to a registry accessible from the host cluster and configure the controller to use it for Kata-based clusters.

An alternative would be an OCI hook to strip the mounts at runtime, but that falls outside the scope of using the Kata project's supplied build artifacts.


## 6. Load Kernel Modules

`vhost_net` and `vhost_vsock` are required for VM networking and host-to-guest communication over vsock:

```bash
modprobe vhost_net
modprobe vhost_vsock
```

To persist these across reboots, add them to `/etc/modules-load.d/kata.conf`.

## 7. Create the RuntimeClass

The `RuntimeClass` name **must start with `kata-`**. This prefix is how the k3k controller identifies Kata-based clusters and applies the necessary pod spec adjustments: stripping `emptyDir` volumes, injecting a `/dev/kmsg` host path mount, and configuring cgroup handling in the K3s startup script.

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
handler: kata-qemu
metadata:
  name: kata-qemu
```

## 8. Create a Cluster

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
  runtimeClassName: kata-qemu
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
