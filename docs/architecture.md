# Architecture

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

