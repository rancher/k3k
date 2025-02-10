# Development

## Tests

To run the tests we use [Ginkgo](https://onsi.github.io/ginkgo/), and [`envtest`](https://book.kubebuilder.io/reference/envtest) for testing the controllers.

Install the required binaries from `envtest` with [`setup-envtest`](https://pkg.go.dev/sigs.k8s.io/controller-runtime/tools/setup-envtest), and then put them in the default path `/usr/local/kubebuilder/bin`:

```
ENVTEST_BIN=$(setup-envtest use -p path)
sudo mkdir -p /usr/local/kubebuilder/bin
sudo cp $ENVTEST_BIN/* /usr/local/kubebuilder/bin
```

then run `ginkgo run ./...`.
