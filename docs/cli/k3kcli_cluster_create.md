## k3kcli cluster create

Create new cluster

```
k3kcli cluster create [flags]
```

### Examples

```
k3kcli cluster create [command options] NAME
```

### Options

```
      --agent-args strings            agents extra arguments
      --agent-envs strings            agents extra Envs
      --agents int                    number of agents
      --cluster-cidr string           cluster CIDR
      --custom-certs string           The path for custom certificate directory
  -h, --help                          help for create
      --kubeconfig-server string      override the kubeconfig server host
      --mirror-host-nodes             Mirror Host Cluster Nodes
      --mode string                   k3k mode type (shared, virtual) (default "shared")
  -n, --namespace string              namespace of the k3k cluster
      --persistence-type string       persistence mode for the nodes (dynamic, ephemeral, static) (default "dynamic")
      --policy string                 The policy to create the cluster in
      --server-args strings           servers extra arguments
      --server-envs strings           servers extra Envs
      --servers int                   number of servers (default 1)
      --service-cidr string           service CIDR
      --storage-class-name string     storage class name for dynamic persistence type
      --storage-request-size string   storage size for dynamic persistence type
      --timeout duration              The timeout for waiting for the cluster to become ready (e.g., 10s, 5m, 1h). (default 3m0s)
      --token string                  token of the cluster
      --version string                k3s version
```

### Options inherited from parent commands

```
      --debug               Turn on debug logs
      --kubeconfig string   kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)
```

### SEE ALSO

* [k3kcli cluster](k3kcli_cluster.md)	 - cluster command

