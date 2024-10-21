package agent

import (
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	configName = "agent-config"
)

type Agent interface {
	Name() string
	Config() (ctrlruntimeclient.Object, error)
	Resources() []ctrlruntimeclient.Object
}

func New(cluster *v1alpha1.Cluster, serviceIP, sharedAgentImage string) Agent {
	if cluster.Spec.Mode == VirtualNodeMode {
		return NewVirtualAgent(cluster, serviceIP)
	}
	return NewSharedAgent(cluster, serviceIP, sharedAgentImage)
}

func configSecretName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, configName)
}
