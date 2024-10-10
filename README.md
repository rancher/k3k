# K3K

[![Experimental](https://img.shields.io/badge/status-experimental-orange.svg)](https://shields.io/)

A Kubernetes in Kubernetes tool, k3k provides a way to run multiple embedded isolated k3s clusters on your kubernetes cluster.

**Experimental Tool**

This project is still under development and is considered experimental. It may have limitations, bugs, or changes. Please use with caution and report any issues you encounter. We appreciate your feedback as we continue to refine and improve this tool.

## Example

An example on creating a k3k cluster on an RKE2 host using k3kcli

[![asciicast](https://asciinema.org/a/eYlc3dsL2pfP2B50i3Ea8MJJp.svg)](https://asciinema.org/a/eYlc3dsL2pfP2B50i3Ea8MJJp)

## Architecture

K3K consists of a controller and a cli tool, the controller can be deployed via a helm chart and the cli can be downloaded from the releases page.

### Controller

The K3K controller will watch a CRD called `clusters.k3k.io`. Once found, the controller will create a separate namespace and it will create a K3S cluster as specified in the spec of the object.

Each server and agent is created as a separate pod that runs in the new namespace.

### CLI

The CLI provides a quick and easy way to create K3K clusters using simple flags, and automatically exposes the K3K clusters so it's accessible via a kubeconfig.

## Features

### Isolation

Each cluster runs in a sperate namespace that can be isolated via netowrk policies and RBAC rules, clusters also run in a sperate network namespace with flannel as the backend CNI. Finally, each cluster has a separate datastore which can be persisted.

In addition, k3k offers a persistence feature that can help users to persist their datatstore, using dynamic storage class volumes.

### Portability and Customization

The "Cluster" object is considered the template of the cluster that you can re-use to spin up multiple clusters in a matter of seconds.

K3K clusters use K3S internally and leverage all options that can be passed to K3S. Each cluster is exposed to the host cluster via NodePort, LoadBalancers, and Ingresses.


|                       | Separate Namespace  (for each tenant) | K3K                          | vcluster        | Separate Cluster (for each tenant) |
|-----------------------|---------------------------------------|------------------------------|-----------------|------------------------------------|
| Isolation             | Very weak                             | Very strong                  | strong          | Very strong                        |
| Access for tenants    | Very restricted                       | Built-in k8s RBAC / Rancher  | Vclustser admin | Cluster admin                      |
| Cost                  | Very cheap                            | Very cheap                   | cheap           | expensive                          |
| Overhead              | Very low                              | Very low                     | Very low        | Very high                          |
| Networking            | Shared                                | Separate                     | shared          | separate                           |
| Cluster Configuration |                                       | Very easy                    | Very hard       |                                    |

## Usage

### Deploy K3K Controller

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

#### For linux and macOS

1 - Donwload the binary, linux dowload url:
```
wget https://github.com/rancher/k3k/releases/download/v0.0.0-alpha2/k3kcli
```
macOS dowload url:
```
wget https://github.com/rancher/k3k/releases/download/v0.0.0-alpha2/k3kcli
```
Then copy to local bin
```
chmod +x k3kcli
sudo cp k3kcli /usr/local/bin
```

#### For Windows 

1 - Download the Binary:
Use PowerShell's Invoke-WebRequest cmdlet to download the binary:
```powershel
Invoke-WebRequest -Uri "https://github.com/rancher/k3k/releases/download/v0.0.0-alpha2/k3kcli-windows" -OutFile "k3kcli.exe"
```
2 - Copy the Binary to a Directory in PATH:
To allow running the binary from any command prompt, you can copy it to a directory in your system's PATH. For example, copying it to C:\Users\<YourUsername>\bin (create this directory if it doesn't exist):
```
Copy-Item "k3kcli.exe" "C:\bin"
```
3 - Update Environment Variable (PATH):
If you haven't already added `C:\bin` (or your chosen directory) to your PATH, you can do it through PowerShell:
```
setx PATH "C:\bin;%PATH%"
```

To create a new cluster you can use:

```sh
k3k cluster create --name example-cluster --token test
```
