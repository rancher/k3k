# K3k Air Gap Installation Guide

Applicable K3k modes: `virtual`, `shared`

This guide describes how to deploy **K3k** in an **air-gapped environment**, including the packaging of required images, Helm chart configurations, and cluster creation using a private container registry.

---

## 1. Package Required Container Images

### 1.1: Follow K3s Air Gap Preparation

Begin with the official K3s air gap packaging instructions:  
[K3s Air Gap Installation Docs](https://docs.k3s.io/installation/airgap)

### 1.2: Include K3k-Specific Images

In addition to the K3s images, make sure to include the following in your image bundle:

| Image Names                 | Descriptions                                                    |
| --------------------------- | --------------------------------------------------------------- |
| `rancher/k3k:<tag>`         | K3k controller image (replace `<tag>` with the desired version) |
| `rancher/k3k-kubelet:<tag>` | K3k agent image for shared mode                                 |
| `rancher/k3s:<tag>`         | K3s server/agent image for virtual clusters                     |

Load these images into your internal (air-gapped) registry.

---

## 2. Configure Helm Chart for Air Gap installation

Update the `values.yaml` file in the K3k Helm chart with air gap settings:

```yaml
image:
  repository: rancher/k3k
  tag: ""            # Specify the version tag
  pullPolicy: ""     # Optional: "IfNotPresent", "Always", etc.

sharedAgent:
  image:
    repository: rancher/k3k-kubelet
    tag: ""          # Specify the version tag
    pullPolicy: ""   # Optional

k3sServer:
  image:
    repository: rancher/k3s
    pullPolicy: ""   # Optional
```

These values enforce the use of internal image repositories for the K3k controller, the agent and the server.

**Note** : All virtual clusters will use automatically those settings. 

---

## 3. Enforce Registry in Virtual Clusters

When creating a virtual cluster, use the `--system-default-registry` flag to ensure all system components (e.g., CoreDNS) pull from your internal registry:

```bash
k3kcli cluster create \
  --server-args "--system-default-registry=registry.internal.domain" \
  my-cluster
```

This flag is passed directly to the K3s server in the virtual cluster, influencing all system workload image pulls.  
[K3s Server CLI Reference](https://docs.k3s.io/cli/server#k3s-server-cli-help)

---

## 4. Specify K3s Version for Virtual Clusters

K3k allows specifying the K3s version used in each virtual cluster:

```bash
k3kcli cluster create \
  --k3s-version v1.29.4+k3s1 \
  my-cluster
```

- If omitted, the **host cluster’s K3s version** will be used by default, which might not exist if it's not part of the air gap package.
