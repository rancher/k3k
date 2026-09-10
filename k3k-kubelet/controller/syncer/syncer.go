// Package syncer mirrors the resources a virtual cluster's pods depend on -
// configmaps, secrets, services, ingresses, persistent volume claims, priority
// classes and events - between the virtual cluster and the host cluster.
package syncer

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

// Context holds the clients and the translator the syncers use to move resources
// between the virtual and the host cluster.
type Context struct {
	ClusterName      string
	ClusterNamespace string
	VirtualClient    client.Client
	HostClient       client.Client
	Translator       translate.ToHostTranslator
}
