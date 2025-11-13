## k3kcli policy create

Create new policy

```
k3kcli policy create [flags]
```

### Examples

```
k3kcli policy create [command options] NAME
```

### Options

```
      --annotations stringArray   Annotations to add to the policy object (e.g. key=value)
  -h, --help                      help for create
      --labels stringArray        Labels to add to the policy object (e.g. key=value)
      --mode string               The allowed mode type of the policy (default "shared")
      --namespace strings         The namespaces where to bind the policy
```

### Options inherited from parent commands

```
      --debug               Turn on debug logs
      --kubeconfig string   kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)
```

### SEE ALSO

* [k3kcli policy](k3kcli_policy.md)	 - policy command

