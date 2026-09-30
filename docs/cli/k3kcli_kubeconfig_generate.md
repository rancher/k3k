## k3kcli kubeconfig generate

Generate kubeconfig for clusters.

```
k3kcli kubeconfig generate [flags]
```

### Examples

```
k3kcli kubeconfig generate [command options] NAME
```

### Options

```
      --altNames strings           altNames of the generated certificates for the kubeconfig
      --cn string                  Common name (CN) of the generated certificates for the kubeconfig (default "system:admin")
      --expiration-days int        Expiration date of the certificates used for the kubeconfig (default 365)
  -h, --help                       help for generate
      --kubeconfig-server string   override the kubeconfig server host
  -n, --namespace string           namespace of the k3k cluster
      --no-out                     do not write a standalone kubeconfig file
      --org strings                Organization name (ORG) of the generated certificates for the kubeconfig
      --out string                 also write a standalone kubeconfig of the cluster to this path
```

### Options inherited from parent commands

```
      --debug               Turn on debug logs
      --kubeconfig string   kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)
```

### SEE ALSO

* [k3kcli kubeconfig](k3kcli_kubeconfig.md)	 - Manage kubeconfig for clusters.

