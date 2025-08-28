package syncer

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

type SyncerContext struct {
	ClusterName      string
	ClusterNamespace string
	VirtualClient    client.Client
	HostClient       client.Client
	Translator       translate.ToHostTranslator
}
