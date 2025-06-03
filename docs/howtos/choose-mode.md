# How to Choose Between Shared and Virtual Mode

This guide helps you choose the right mode for your virtual cluster: **Shared** or **Virtual**.  
If you're unsure, start with **Shared mode** — it's the default and fits most common scenarios.

---

## Shared Mode (default)

**Best for:**
- Developers who want to run workloads quickly without managing Kubernetes internals
- Platform teams that require visibility and control over all workloads
- Users who need access to host-level resources (e.g., GPUs)

In **Shared mode**, the virtual cluster runs its own K3s server but relies on the host to execute workloads. The virtual kubelet syncs resources, enabling lightweight, fast provisioning with support for cluster resource isolation. More details on the [architecture](./../architecture.md#shared-mode). 

---

### Use Cases by Persona

#### 👩‍💻 Developer  
*"I’m building a web app that should be exposed outside the virtual cluster."*  
→ Use **Shared mode**. It allows you to [expose](./expose-workloads.md) your application.

#### 👩‍🔬 Data Scientist:
*“I need to run Jupyter notebooks that leverage the cluster's GPU.”*  
→ Use **Shared mode**. It gives access to physical devices while keeping overhead low.

#### 🧑‍💼 Platform Admin  
*"I want to monitor and secure all tenant workloads from a central location."*  
→ Use **Shared mode**. Host-level agents (e.g., observability, policy enforcement) work across all virtual clusters.

#### 🔒 Security Engineer  
*"I need to enforce security policies like network policies or runtime scanning across all workloads."*  
→ Use **Shared mode**. The platform can enforce policies globally without tenant bypass.

*"I need to test a new admission controller or policy engine."*  
→ Use **Shared mode**, if it's scoped to your virtual cluster. You can run tools like Kubewarden without affecting the host.  

#### 🔁 CI/CD Engineer  
*"I want to spin up disposable virtual clusters per pipeline run, fast and with low resource cost."*  
→ Use **Shared mode**. It's quick to provision and ideal for short-lived, namespace-scoped environments.

---

## Virtual Mode

**Best for:**
- Advanced users who need full Kubernetes isolation
- Developers testing experimental or cluster-wide features
- Use cases requiring control over the entire Kubernetes control plane

In **Virtual mode**, the virtual cluster runs its own isolated Kubernetes control plane. It supports different CNIs, and API configurations — ideal for deep experimentation or advanced workloads. More details on the [architecture](./../architecture.md#virtual-mode). 

---

### Use Cases by Persona

#### 👩‍💻 Developer  
*"I need to test a new Kubernetes feature gate that’s disabled in the host cluster."*  
→ Use **Virtual mode**. You can configure your own control plane flags and API features.

#### 🧑‍💼 Platform Admin  
*"We’re testing upgrades across Kubernetes versions, including new API behaviors."*
→ Use Virtual mode. You can run different Kubernetes versions and safely validate upgrade paths.

#### 🌐 Network Engineer  
*"I’m evaluating a new CNI that needs full control of the cluster’s networking."*  
→ Use **Virtual mode**. You can run a separate CNI stack without affecting the host or other tenants.

#### 🔒 Security Engineer  
*"I’m testing a new admission controller and policy engine before rolling it out cluster-wide."*  
→ Use **Virtual mode**, if you need to test cluster-wide policies, custom admission flow, or advanced extensions with full control.

---

## Still Not Sure?

If you're evaluating more advanced use cases or want a deeper comparison, see the full trade-off breakdown in the [Architecture documentation](../architecture.md).