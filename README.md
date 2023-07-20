# K3K

A Kubernetes in Kubernetes tool, k3k provides a way to run multiple embedded isolated k3s clusters on your kubernetes cluster.

## Example

An example on creating a k3k cluster on an RKE2 host using k3kcli

[![asciicast](https://asciinema.org/a/eYlc3dsL2pfP2B50i3Ea8MJJp.svg)](https://asciinema.org/a/eYlc3dsL2pfP2B50i3Ea8MJJp)

## Usage

K3K consists of a controller and a cli tool, the controller can be deployed via a helm chart and the cli can be downloaded from the releases page.

### Deploy Controller

[Helm](https://helm.sh) must be installed to use the charts.  Please refer to
Helm's [documentation](https://helm.sh/docs) to get started.

Once Helm has been set up correctly, add the repo as follows:

```sh
  helm repo add k3k https://rancher.github.io/k3k
```

If you had already added this repo earlier, run `helm repo update` to retrieve
the latest versions of the packages.  You can then run `helm search repo
k3k --devel` to see the charts.

To install the k3k chart:

```sh
helm install my-k3k k3k/k3k --devel
```

To uninstall the chart:

```sh
helm delete my-k3k
```

**NOTE: Since k3k is still under development, the chart is marked as a development chart, this means that you need to add the `--devel` flag to install it.**

### Create a new cluster

To create a new cluster you need to install and run the cli or create a cluster object, to install the cli:

```sh
wget https://github.com/rancher/k3k/releases/download/v0.0.0-alpha6/k3kcli
chmod +x k3kcli
sudo cp k3kcli /usr/local/bin
```

To create a new cluster you can use:

```sh
k3k cluster create --name example-cluster --token test
```
