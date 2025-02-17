# Development


## Prerequisites

To start developing K3k you will need:

- Go
- Docker
- Helm
- A running Kubernetes cluster


### TLDR

```shell
#!/bin/bash

set -euo pipefail

# These environment variables configure the image repository and tag.
export REPO=ghcr.io/myuser
export VERSION=dev-$(date -u '+%Y%m%d%H%M')

make
make push
make install
```

### Makefile

To see all the available Make commands you can run `make help`, i.e:

```
-> % make help
  all                            Run 'make' or 'make all' to run 'version', 'build' and 'package'
  version                        Print the current version
  build                          Build the the K3k binaries (k3k, k3k-kubelet and k3kcli)
  package                        Package the k3k and k3k-kubelet Docker images
  push                           Push the K3k images to the registry
  test                           Run all the tests
  test-unit                      Run the unit tests (skips the e2e)
  test-controller                Run the controller tests (pkg/controller)
  test-e2e                       Run the e2e tests
  build-crds                     Build the CRDs specs
  docs                           Build the CRDs docs
  lint                           Find any linting issues in the project
  validate                       Validate the project checking for any dependency or doc mismatch
  install                        Install K3k with Helm on the targeted Kubernetes cluster
  help                           Show this help.
```

### Build

To build the needed binaries (`k3k`, `k3k-kubelet` and the `k3kcli`) and package the images you can simply run `make`.

By default the `rancher` repository will be used, but you can customize this to your registry with the `REPO` env var:

```
REPO=ghcr.io/userorg make
```

To customize the tag you can also explicitly set the VERSION:

```
VERSION=dev-$(date -u '+%Y%m%d%H%M') make
```


### Push

You will need to push the built images to your registry, and you can use the `make push` command to do this.


### Install

Once you have your images available you can install K3k with the `make install` command. This will use `helm` to install the release.


## Tests

To run the tests you can just run `make test`, or one of the other available "sub-tests" targets (`test-unit`, `test-controller`, `test-e2e`).

We use [Ginkgo](https://onsi.github.io/ginkgo/), and [`envtest`](https://book.kubebuilder.io/reference/envtest) for testing the controllers.

The required binaries for `envtest` are installed with [`setup-envtest`](https://pkg.go.dev/sigs.k8s.io/controller-runtime/tools/setup-envtest), in the `.envtest` folder.


## CRDs and Docs

We are unsing Kubebuilde and `controller-gen` to build the needed CRDs. To generate the specs you can run `make build-crds`.

Remember also to update the CRDs documentation running the `make docs` command.
