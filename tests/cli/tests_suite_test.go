package cli_test

import (
	"context"
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fwclient "github.com/rancher/k3k/tests/framework/client"
	fwcontainer "github.com/rancher/k3k/tests/framework/container"
	fwlog "github.com/rancher/k3k/tests/framework/log"

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
	k3sContainer   *k3s.K3sContainer
	restcfg        *rest.Config
	k8s            *kubernetes.Clientset
	k8sClient      client.Client
	kubeconfigPath string
)

var _ = BeforeSuite(func() {
	ctx := context.Background()

	_, dockerInstallEnabled := os.LookupEnv("K3K_DOCKER_INSTALL")

	if dockerInstallEnabled {
		repo := os.Getenv("REPO")
		if repo == "" {
			repo = "rancher"
		}

		installK3SDocker(ctx, repo+"/k3k", repo+"/k3k-kubelet")
		initKubernetesClient(ctx)
		installK3kChart(repo+"/k3k", repo+"/k3k-kubelet")
	} else {
		initKubernetesClient(ctx)
	}
})

func installK3SDocker(ctx context.Context, controllerImage, kubeletImage string) {
	k3sHostVersion := os.Getenv("K3S_HOST_VERSION")
	if k3sHostVersion == "" {
		k3sHostVersion = k3sVersion
	}

	k3sContainer, kubeconfigPath = fwcontainer.SetupK3s(ctx, k3sHostVersion, controllerImage, kubeletImage)
}

func initKubernetesClient(ctx context.Context) {
	scheme := fwclient.NewScheme()
	config, err := fwclient.InitFromKubeconfig(ctx, scheme, k3sContainer)
	Expect(err).NotTo(HaveOccurred())

	restcfg = config.RestConfig
	k8s = config.Clientset
	k8sClient = config.Client
}

func installK3kChart(controllerImage, kubeletImage string) {
	installer := fwcontainer.NewHelmInstaller(controllerImage, kubeletImage, kubeconfigPath)
	kubeconfig, err := os.ReadFile(kubeconfigPath)
	Expect(err).NotTo(HaveOccurred())

	restClientGetter, err := fwclient.NewRESTClientGetter(kubeconfig)
	Expect(err).NotTo(HaveOccurred())

	installer.InstallK3kChart(restClientGetter)
}

var _ = AfterSuite(func() {
	ctx := context.Background()

	if k3sContainer != nil {
		// dump k3s logs
		k3sLogs, err := k3sContainer.Logs(ctx)
		Expect(err).To(Not(HaveOccurred()))
		fwlog.WriteToTemp("k3s.log", k3sLogs)

		// dump k3k controller logs
		k3kLogs := fwlog.GetK3kPodLogs(ctx, k8sClient, k8s, k3kNamespace)
		fwlog.WriteToTemp("k3k.log", k3kLogs)

		testcontainers.CleanupContainer(GinkgoTB(), k3sContainer)
	}
})
