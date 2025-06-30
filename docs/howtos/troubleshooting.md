# Troubleshooting

This guide walks through common troubleshooting steps for working with K3K virtual clusters.

---

## `too many open files` error

The `k3k-kubelet` or `k3kcluster-server-` run into the following issue:  

```sh
E0604 13:14:53.369369       1 leaderelection.go:336] error initially creating leader election record: Post "https://k3k-http-proxy-k3kcluster-service/apis/coordination.k8s.io/v1/namespaces/kube-system/leases": context canceled
{"level":"fatal","timestamp":"2025-06-04T13:14:53.369Z","logger":"k3k-kubelet","msg":"virtual manager stopped","error":"too many open files"}
```

This typically indicates a low limit on inotify watchers or file descriptors on the host system.

To increase the inotify limits connect to the host nodes and run:

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
kubectl logs -n <cluster_namespace> -l cluster=<cluster_name>
```

This retrieves logs from K3k cluster components (`agents, server and virtual-kubelet`).

> 💡 You can also use `kubectl describe cluster <cluster_name>` to check for recent events and status conditions.

---

## Virtual Cluster Not Starting or Stuck in Pending

Some of the most common causes are related to missing prerequisites or wrong configuration.

### Storage class not available

When creating a Virtual Cluster with `dynamic` persistence, a PVC is needed. You can check if the PVC was claimed but not bound with `kubectl get pvc -n <cluster_namespace>`. If you see a pending PVC you probably don't have a default storage class defined, or you have specified a wrong one.

#### Example with wrong storage class

The `pvc` is pending:

```bash
kubectl get pvc -n k3k-test-storage
NAME                                         STATUS    VOLUME   CAPACITY   ACCESS MODES   STORAGECLASS    VOLUMEATTRIBUTESCLASS   AGE
varlibrancherk3s-k3k-test-storage-server-0   Pending                                      not-available   <unset>                 4s
```

The `server` is pending:

```bash
kubectl get po -n k3k-test-storage
NAME                             READY   STATUS    RESTARTS   AGE
k3k-test-storage-kubelet-j4zn5   1/1     Running   0          54s
k3k-test-storage-server-0        0/1     Pending   0          54s
```

To fix this you should use a valid storage class, you can list existing storage class using:

```bash
kubectl get storageclasses.storage.k8s.io
NAME                   PROVISIONER             RECLAIMPOLICY   VOLUMEBINDINGMODE      ALLOWVOLUMEEXPANSION   AGE
local-path (default)   rancher.io/local-path   Delete          WaitForFirstConsumer   false                  3d6h
```

### Wrong node selector

When creating a Virtual Cluster with `defaultNodeSelector`, if the selector is not valid all pods will be pending.

#### Example

The `server` is pending:

```bash
kubectl get po
NAME                                  READY   STATUS    RESTARTS   AGE
k3k-k3kcluster-node-placed-server-0   0/1     Pending   0          58s
```

The description of the pod provide the reason:

```bash
kubectl describe po k3k-k3kcluster-node-placed-server-0
...
Events:
  Type     Reason            Age   From               Message
  ----     ------            ----  ----               -------
  Warning  FailedScheduling  84s   default-scheduler  0/1 nodes are available: 1 node(s) didn't match Pod's node affinity/selector. preemption: 0/1 nodes are available: 1 Preemption is not helpful for scheduling.
```

To fix this you should use a valid node affinity/selector.

### Image pull issues (airgapped setup)

When creating a Virtual Cluster in air-gapped environment, images need to be available in the configured registry. You can check for `ImagePullBackOff` status when getting the pods in the virtual cluster namespace.

#### Example

The `server` is failing:

```bash
kubectl get po -n k3k-test-registry
NAME                             READY   STATUS          RESTARTS       AGE
k3k-test-registry-kubelet-r4zh5   1/1     Running            0          54s
k3k-test-registry-server-0        0/1     ImagePullBackOff   0          54s
```

To fix this make sure the failing image is available. You can describe the failing pod to get more details.
