package upgrade_test

import (
	"context"
	"os"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	sigsclient "sigs.k8s.io/controller-runtime/pkg/client"

	fwclient "github.com/rancher/k3k/tests/framework/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	k3kNamespace         = "k3k-system"
	k3kUpgradeTestsLabel = "k3k-upgrade"
	slowTestsLabel       = "slow"
)

var (
	hostIP    string
	restcfg   *rest.Config
	k8s       *kubernetes.Clientset
	k8sClient sigsclient.Client
)

func TestUpgrade(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgrade Suite")
}

var _ = BeforeSuite(func() {
	ctx := context.Background()

	GinkgoWriter.Println("GOCOVERDIR:", os.Getenv("GOCOVERDIR"))

	scheme := fwclient.NewScheme()
	config, err := fwclient.InitFromKubeconfig(ctx, scheme)
	Expect(err).NotTo(HaveOccurred())

	hostIP = config.HostIP
	restcfg = config.RestConfig
	k8s = config.Clientset
	k8sClient = config.Client

	GinkgoWriter.Println("Host IP: " + hostIP)
})
