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
      --agents int32              number of agents
      --annotations stringArray   Annotations to add to the cluster object (e.g. key=value)
  -h, --help                      help for update
      --labels stringArray        Labels to add to the cluster object (e.g. key=value)
  -n, --namespace string          namespace of the k3k cluster
  -y, --no-confirm                Skip interactive approval before applying update
      --servers int32             number of servers (default 1)
      --version string            k3s version
```

### Options inherited from parent commands

```
      --debug               Turn on debug logs
      --kubeconfig string   kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)
```

### SEE ALSO

* [k3kcli cluster](k3kcli_cluster.md)	 - K3k cluster command.

