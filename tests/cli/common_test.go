package cli_test

import (
	v1 "k8s.io/api/core/v1"

	fwk3k "github.com/rancher/k3k/tests/framework/k3k"
)

func NewNamespace() *v1.Namespace {
	return fwk3k.CreateNamespace(k8s)
}

func DeleteNamespaces(names ...string) {
	fwk3k.DeleteNamespaces(k8s, names...)
}
