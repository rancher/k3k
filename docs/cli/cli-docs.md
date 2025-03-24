# NAME

k3kcli - CLI for K3K

# SYNOPSIS

k3kcli

```
[--debug]
```

**Usage**:

```
k3kcli [GLOBAL OPTIONS] command [COMMAND OPTIONS] [ARGUMENTS...]
```

# GLOBAL OPTIONS

**--debug**: Turn on debug logs


# COMMANDS

## cluster

cluster command

### create

Create new cluster

>k3kcli cluster create [command options] NAME

**--agent-args**="": agents extra arguments

**--agents**="": number of agents (default: 0)

**--cluster-cidr**="": cluster CIDR

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

**--kubeconfig-server**="": override the kubeconfig server host

**--mode**="": k3k mode type (shared, virtual) (default: "shared")

**--namespace**="": namespace to create the k3k cluster in

**--persistence-type**="": persistence mode for the nodes (dynamic, ephemeral, static) (default: "dynamic")

**--server-args**="": servers extra arguments

**--servers**="": number of servers (default: 1)

**--service-cidr**="": service CIDR

**--storage-class-name**="": storage class name for dynamic persistence type

**--token**="": token of the cluster

**--version**="": k3s version

### delete

Delete an existing cluster

>k3kcli cluster delete [command options] NAME

**--keep-data**: keeps persistence volumes created for the cluster after deletion

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

**--namespace**="": namespace to create the k3k cluster in

## kubeconfig

Manage kubeconfig for clusters

### generate

Generate kubeconfig for clusters

**--altNames**="": altNames of the generated certificates for the kubeconfig

**--cn**="": Common name (CN) of the generated certificates for the kubeconfig (default: "system:admin")

**--config-name**="": the name of the generated kubeconfig file

**--expiration-days**="": Expiration date of the certificates used for the kubeconfig (default: 356)

**--kubeconfig**="": kubeconfig path (default: $HOME/.kube/config or $KUBECONFIG if set)

**--kubeconfig-server**="": override the kubeconfig server host

**--name**="": cluster name

**--namespace**="": namespace to create the k3k cluster in

**--org**="": Organization name (ORG) of the generated certificates for the kubeconfig
