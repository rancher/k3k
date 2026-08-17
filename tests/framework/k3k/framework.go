package k3k

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	fwclient "github.com/rancher/k3k/tests/framework/client"
)

// Framework bundles the host cluster clients needed by the k3k test helpers.
// Every method acts on the host cluster, or on a virtual cluster created through it.
//
// The helpers of this package use Ginkgo/Gomega assertions, so they must only be
// called from within a spec, after RegisterFailHandler has been called.
type Framework struct {
	HostIP     string
	RestConfig *rest.Config
	Clientset  *kubernetes.Clientset
	Client     ctrlruntimeclient.Client
}

// New builds a Framework from the client config returned by client.InitFromKubeconfig.
func New(cfg *fwclient.Config) *Framework {
	return &Framework{
		HostIP:     cfg.HostIP,
		RestConfig: cfg.RestConfig,
		Clientset:  cfg.Clientset,
		Client:     cfg.Client,
	}
}
