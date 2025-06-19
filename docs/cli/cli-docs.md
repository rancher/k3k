# NAME

k3kcli - CLI for K3K

# SYNOPSIS

k3kcli

```
[--debug]
[--kubeconfig]=[value]
```

**Usage**:

```
k3kcli [GLOBAL OPTIONS] command [COMMAND OPTIONS] [ARGUMENTS...]
```

# GLOBAL OPTIONS

**--debug**: Turn on debug logs

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)


# COMMANDS

## cluster

cluster command

### create

Create new cluster

>k3kcli cluster create [command options] NAME

**--agent-args**="": agents extra arguments

**--agent-envs**="": agents extra Envs

**--agents**="": number of agents (default: 0)

**--cluster-cidr**="": cluster CIDR

**--debug**: Turn on debug logs

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

**--kubeconfig-server**="": override the kubeconfig server host

**--mode**="": k3k mode type (shared, virtual) (default: "shared")

**--namespace, -n**="": namespace of the k3k cluster

**--persistence-type**="": persistence mode for the nodes (dynamic, ephemeral, static) (default: "dynamic")

**--policy**="": The policy to create the cluster in

**--server-args**="": servers extra arguments

**--server-envs**="": servers extra Envs

**--servers**="": number of servers (default: 1)

**--service-cidr**="": service CIDR

**--storage-class-name**="": storage class name for dynamic persistence type

**--token**="": token of the cluster

**--version**="": k3s version

### delete

Delete an existing cluster

>k3kcli cluster delete [command options] NAME

**--debug**: Turn on debug logs

**--keep-data**: keeps persistence volumes created for the cluster after deletion

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

**--namespace, -n**="": namespace of the k3k cluster

### list

List all the existing cluster

>k3kcli cluster list [command options]

**--debug**: Turn on debug logs

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

**--namespace, -n**="": namespace of the k3k cluster

## policy

policy command

### create

Create new policy

>k3kcli policy create [command options] NAME

**--debug**: Turn on debug logs

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

**--mode**="": The allowed mode type of the policy (default: "shared")

### delete

Delete an existing policy

>k3kcli policy delete [command options] NAME

**--debug**: Turn on debug logs

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

### list

List all the existing policies

>k3kcli policy list [command options]

**--debug**: Turn on debug logs

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

## kubeconfig

Manage kubeconfig for clusters

### generate

Generate kubeconfig for clusters

**--altNames**="": altNames of the generated certificates for the kubeconfig

**--cn**="": Common name (CN) of the generated certificates for the kubeconfig (default: "system:admin")

**--config-name**="": the name of the generated kubeconfig file

**--debug**: Turn on debug logs

**--expiration-days**="": Expiration date of the certificates used for the kubeconfig (default: 356)

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

**--kubeconfig-server**="": override the kubeconfig server host

**--name**="": cluster name

**--namespace, -n**="": namespace of the k3k cluster

**--org**="": Organization name (ORG) of the generated certificates for the kubeconfig
