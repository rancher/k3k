# How to install k3k on k3d

This document provides a guide on how to install k3k on [k3d](https://k3d.io).

## Installing k3d

Since k3d uses docker under the hood, we need to expose the ports on the host that we'll then use for the NodePort in virtual cluster creation.

Create the k3d cluster in the following way:

```bash
k3d cluster create k3k -p "30000-30010:30000-30010@server:0"
```

With this syntax ports from 30000 to 20010 will be exposed on the host.

## Install k3k

Install now k3k as usual:

```bash
helm repo update
helm install --namespace k3k-system --create-namespace k3k k3k/k3k --devel
```

## Create a virtual cluster

Once the k3k controller is up and running, create a namespace where to create our first virtual cluster.

```bash
kubectl create ns k3k-mycluster
```

Create then the virtual cluster exposing through NodePort one of the ports that we set up in the previous step:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: k3k.io/v1alpha1
kind: Cluster
metadata:
  name: mycluster
  namespace: k3k-mycluster
spec:    
  expose:
    nodePort:
      serverPort: 30001
EOF
```

Check when the cluster is ready:

```bash
kubectl get po -n k3k-mycluster
```

Last thing to do is to get the kubeconfig to connect to the virtual cluster we've just created:

```bash
k3kcli kubeconfig generate --name mycluster --namespace k3k-mycluster --kubeconfig-server localhost:30001
```
