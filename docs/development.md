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
