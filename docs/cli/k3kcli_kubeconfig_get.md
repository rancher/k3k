## k3kcli kubeconfig get

Fetch and export kubeconfig for a cluster

### Synopsis

Fetches the kubeconfig from the cluster's secret and writes it to a file. Optionally override the server host with --kubeconfig-server for external access.

```
k3kcli kubeconfig get [flags]
```

### Options

```
      --config-name string         the name of the generated kubeconfig file
  -h, --help                       help for get
      --kubeconfig-server string   override the kubeconfig server host
      --name string                cluster name
  -n, --namespace string           namespace of the k3k cluster
```

### Options inherited from parent commands

```
      --debug               Turn on debug logs
      --kubeconfig string   kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)
```

### SEE ALSO

* [k3kcli kubeconfig](k3kcli_kubeconfig.md)	 - Manage kubeconfig for clusters.

