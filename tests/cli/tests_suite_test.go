package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fwclient "github.com/rancher/k3k/tests/framework/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	k3sVersion    = "v1.36.2-k3s1"
	k3sOldVersion = "v1.36.0-k3s1"
)

func TestTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tests Suite")
}

var (
	restcfg   *rest.Config
	k8s       *kubernetes.Clientset
	k8sClient client.Client

	// kubeconfigPath is a copy of the kubeconfig of the host cluster, used as the $KUBECONFIG
	// of every command run by the suite.
	//
	// k3kcli adds a context to the kubeconfig it reads, so the suite must never be pointed at
	// the real kubeconfig of the developer or of the CI runner.
	kubeconfigPath string
)

var _ = BeforeSuite(func() {
	ctx := context.Background()

	initKubernetesClient(ctx)

	kubeconfigPath = copyHostKubeconfig()
})

// copyHostKubeconfig copies the kubeconfig of the host cluster to a temporary file, returning
// its path.
func copyHostKubeconfig() string {
	GinkgoHelper()

	data, err := os.ReadFile(os.Getenv("KUBECONFIG"))
	Expect(err).To(Not(HaveOccurred()))

	dir, err := os.MkdirTemp("", "k3k-cli")
	Expect(err).To(Not(HaveOccurred()))

	DeferCleanup(func() {
		_ = os.RemoveAll(dir)
	})

	path := filepath.Join(dir, "config")
	Expect(os.WriteFile(path, data, 0o600)).To(Succeed())

	return path
}

func initKubernetesClient(ctx context.Context) {
	scheme := fwclient.NewScheme()
	config, err := fwclient.InitFromKubeconfig(ctx, scheme)
	Expect(err).NotTo(HaveOccurred())

	restcfg = config.RestConfig
	k8s = config.Clientset
	k8sClient = config.Client
}
