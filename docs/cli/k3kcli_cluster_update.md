## k3kcli cluster update

Update existing cluster

```
k3kcli cluster update [flags]
```

### Examples

```
k3kcli cluster update [command options] NAME
```

### Options

```
      --agent-args strings         agents extra arguments
      --agent-envs strings         agents extra Envs
      --agents int                 number of agents
      --annotations stringArray    Annotations to add to the cluster object (e.g. key=value)
  -h, --help                       help for update
      --kubeconfig-server string   override the kubeconfig server host
      --labels stringArray         Labels to add to the cluster object (e.g. key=value)
  -n, --namespace string           namespace of the k3k cluster
      --server-args strings        servers extra arguments
      --server-envs strings        servers extra Envs
      --servers int                number of servers (default 1)
      --timeout duration           The timeout for waiting for the cluster to become ready (e.g., 10s, 5m, 1h). (default 3m0s)
      --version string             k3s version
```

### Options inherited from parent commands

```
      --debug               Turn on debug logs
      --kubeconfig string   kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)
```

### SEE ALSO

* [k3kcli cluster](k3kcli_cluster.md)	 - K3k cluster command.

