package agent

import (
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/config"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Agent interface {
	Config() (ctrlruntimeclient.Object, error)
	Resources() ([]ctrlruntimeclient.Object, error)
}

func New(cluster *v1alpha1.Cluster, serviceIP string) Agent {
	if cluster.Spec.Mode == config.VirtualNodeMode {
		return NewVirtualAgent(cluster, serviceIP)
	} else {
		return NewSharedAgent(cluster, serviceIP)
	}
}
