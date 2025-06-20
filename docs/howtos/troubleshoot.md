# How to: Troubleshoot K3K

This guide walks through common troubleshooting steps for working with K3K virtual clusters.

---

## Virtual Cluster Fails with “too many open files” Error

### Symptom

The `k3k-kubelet` or `k3kcluster-server-` run into the following issue:  

```sh
E0604 13:14:53.369369       1 leaderelection.go:336] error initially creating leader election record: Post "https://k3k-http-proxy-k3kcluster-service/apis/coordination.k8s.io/v1/namespaces/kube-system/leases": context canceled
{"level":"fatal","timestamp":"2025-06-04T13:14:53.369Z","logger":"k3k-kubelet","msg":"virtual manager stopped","error":"too many open files"}
```

This typically indicates a low limit on inotify watchers or file descriptors on the host system.

### Solution: Adjust Host System Settings

Connect to the host nodes and increase inotify limits:

```sh
sudo sysctl -w fs.inotify.max_user_watches=2099999999
sudo sysctl -w fs.inotify.max_user_instances=2099999999
sudo sysctl -w fs.inotify.max_queued_events=2099999999
```

You can persist these settings by adding them to `/etc/sysctl.conf`:

```sh
fs.inotify.max_user_watches=2099999999
fs.inotify.max_user_instances=2099999999
fs.inotify.max_queued_events=2099999999
```

Apply the changes:

```sh
sudo sysctl -p
```

You can find more details in this [KB document](https://www.suse.com/support/kb/doc/?id=000020048).

---

## Inspect Controller Logs for Failure Diagnosis

To view logs for a failed virtual cluster:

```sh
kubectl logs -n k3k-system -l app.kubernetes.io/name=k3k
```

This retrieves logs from K3k controller components.

---

## Inspect Cluster Logs for Failure Diagnosis

To view logs for a failed virtual cluster:

```sh
kubectl logs -n k3k-<cluster_name> -l cluster=<cluster_name>
```

This retrieves logs from K3k cluster components (`agents, server and virtual-kubelet`).

> 💡 You can also use `kubectl describe cluster <cluster_name>` to check for recent events and status conditions.

---

## Virtual Cluster Not Starting or Stuck in Pending

### Common Causes

- Storage class not available
- Insufficient node resources
- Wrong node selector
- Image pull issues (airgapped setup)

### Solution

Check the associated pods in the K3K system namespace:

```sh
kubectl get pods -n <cluster_namespace>
kubectl describe pod <pod-name> -n k3k-system
```

Look for events like `FailedScheduling`, `ImagePullBackOff`, or `VolumeBindingFailed`.

---

## Troubleshoot Cluster Networking Issues

If the virtual cluster cannot reach external services or internal DNS isn't resolving, inspect CNI setup and DNS.

```sh
kubectl exec -n k3k-system <pod-name> -- nslookup kubernetes.default
kubectl get pods -n kube-system -l k8s-app=kube-dns
```

Ensure CNI and DNS pods are healthy and check for network policies blocking traffic.

---

## Inspect Cluster Resource Usage

Virtual clusters may fail silently if resource quotas or limits are exceeded.

```sh
kubectl get resourcequotas -n <cluster_namespace>
kubectl describe node
```

This helps identify memory, CPU, or disk exhaustion.
