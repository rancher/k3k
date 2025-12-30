## k3kcli cluster delete

Delete an existing cluster.

```
k3kcli cluster delete [flags]
```

### Examples

```
k3kcli cluster delete [command options] NAME
```

### Options

```
  -h, --help               help for delete
      --keep-data          keeps persistence volumes created for the cluster after deletion
  -n, --namespace string   namespace of the k3k cluster
```

### Options inherited from parent commands

```
      --debug               Turn on debug logs
      --kubeconfig string   kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)
```

### SEE ALSO

* [k3kcli cluster](k3kcli_cluster.md)	 - K3k cluster command.

