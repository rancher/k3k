package upgrade_test

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	fwclient "github.com/rancher/k3k/tests/framework/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const k3kNamespace = "k3k-system"

var (
	hostIP    string
	k8s       *kubernetes.Clientset
	k8sClient ctrlruntimeclient.Client
)

func TestUpgrade(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgrade Suite")
}

var _ = BeforeSuite(func() {
	ctx := context.Background()

	scheme := fwclient.NewScheme()
	config, err := fwclient.InitFromKubeconfig(ctx, scheme)
	Expect(err).NotTo(HaveOccurred())

	hostIP = config.HostIP
	k8s = config.Clientset
	k8sClient = config.Client

	GinkgoWriter.Println("Host IP: " + hostIP)
})
