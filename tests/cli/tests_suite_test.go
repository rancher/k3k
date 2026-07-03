package cli_test

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fwclient "github.com/rancher/k3k/tests/framework/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	k3kNamespace = "k3k-system"

	k3sVersion    = "v1.35.2-k3s1"
	k3sOldVersion = "v1.35.0-k3s1"
)

func TestTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tests Suite")
}

var (
	restcfg   *rest.Config
	k8s       *kubernetes.Clientset
	k8sClient client.Client
)

var _ = BeforeSuite(func() {
	ctx := context.Background()

	initKubernetesClient(ctx)
})

func initKubernetesClient(ctx context.Context) {
	scheme := fwclient.NewScheme()
	config, err := fwclient.InitFromKubeconfig(ctx, scheme)
	Expect(err).NotTo(HaveOccurred())

	restcfg = config.RestConfig
	k8s = config.Clientset
	k8sClient = config.Client
}
