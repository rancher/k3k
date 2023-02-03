# K3K

A Kubernetes in Kubernetes tool, k3k provides a way to run multiple embedded isolated k3s clusters on your kubernetes cluster.

## Why?

![alt text](https://github.com/galal-hussein/k3k/blob/main/hack/becausewecan.jpg?raw=true)


## Usage

K3K consists of a controller and a cli tool, the controller can be deployed via a helm chart and the cli can be downloaded from the releases page.

### Deploy Controller

[Helm](https://helm.sh) must be installed to use the charts.  Please refer to
Helm's [documentation](https://helm.sh/docs) to get started.

Once Helm has been set up correctly, add the repo as follows:

  helm repo add k3k https://galal-hussein.github.io/k3k

If you had already added this repo earlier, run `helm repo update` to retrieve
the latest versions of the packages.  You can then run `helm search repo
k3k` to see the charts.

To install the k3k chart:

    helm install my-k3k k3k/k3k

To uninstall the chart:

    helm delete my-k3k

### Create a new cluster

To create a new cluster you need to install and run the cli or create a cluster object, to install the cli:

```
wget https://github.com/galal-hussein/k3k/releases/download/v0.0.0-alpha2/k3k
chmod +x k3k
sudo cp k3k /usr/local/bin
```

To create a new cluster you can use:
```
k3k cluster create --name example-cluster --token test
```