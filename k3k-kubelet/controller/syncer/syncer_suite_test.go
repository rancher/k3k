package syncer_test

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster Controller Suite")
}

type TestEnv struct {
	*envtest.Environment
	k8s       *kubernetes.Clientset
	k8sClient client.Client
}

var (
	hostTestEnv *TestEnv
	hostManager ctrl.Manager
	virtTestEnv *TestEnv
	virtManager ctrl.Manager
)

var _ = BeforeSuite(func() {
	hostTestEnv = NewTestEnv()
	By("HOST testEnv running at :" + hostTestEnv.ControlPlane.APIServer.Port)

	virtTestEnv = NewTestEnv()
	By("VIRT testEnv running at :" + virtTestEnv.ControlPlane.APIServer.Port)

	ctrl.SetLogger(zapr.NewLogger(zap.NewNop()))
	ctrl.SetupSignalHandler()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")

	err := hostTestEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

	err = virtTestEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

	tmpKubebuilderDir := path.Join(os.TempDir(), "kubebuilder")
	err = os.RemoveAll(tmpKubebuilderDir)
	Expect(err).NotTo(HaveOccurred())
})

func NewTestEnv() *TestEnv {
	GinkgoHelper()

	binaryAssetsDirectory := os.Getenv("KUBEBUILDER_ASSETS")
	if binaryAssetsDirectory == "" {
		binaryAssetsDirectory = "/usr/local/kubebuilder/bin"
	}

	tmpKubebuilderDir := path.Join(os.TempDir(), "kubebuilder")

	if err := os.Mkdir(tmpKubebuilderDir, 0o755); !errors.Is(err, os.ErrExist) {
		Expect(err).NotTo(HaveOccurred())
	}

	tempDir, err := os.MkdirTemp(tmpKubebuilderDir, "envtest-*")
	Expect(err).NotTo(HaveOccurred())

	err = os.CopyFS(tempDir, os.DirFS(binaryAssetsDirectory))
	Expect(err).NotTo(HaveOccurred())

	By("bootstrapping test environment")

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "charts", "k3k", "crds")},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: tempDir,
		Scheme:                buildScheme(),
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	k8s, err := kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err := client.New(cfg, client.Options{Scheme: testEnv.Scheme})
	Expect(err).NotTo(HaveOccurred())

	return &TestEnv{
		Environment: testEnv,
		k8s:         k8s,
		k8sClient:   k8sClient,
	}
}

func buildScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	err := clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = v1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	return scheme
}

var _ = Describe("Kubelet Controller", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		var err error
		ctx, cancel = context.WithCancel(context.Background())

		hostManager, err = ctrl.NewManager(hostTestEnv.Config, ctrl.Options{
			// disable the metrics server
			Metrics: metricsserver.Options{BindAddress: "0"},
			Scheme:  hostTestEnv.Scheme,
		})
		Expect(err).NotTo(HaveOccurred())

		virtManager, err = ctrl.NewManager(virtTestEnv.Config, ctrl.Options{
			// disable the metrics server
			Metrics: metricsserver.Options{BindAddress: "0"},
			Scheme:  virtTestEnv.Scheme,
		})
		Expect(err).NotTo(HaveOccurred())

		go func() {
			defer GinkgoRecover()
			err := hostManager.Start(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to run host manager")
		}()

		go func() {
			defer GinkgoRecover()
			err := virtManager.Start(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to run virt manager")
		}()
	})

	AfterEach(func() {
		cancel()
	})

	Describe("PriorityClass Syncer", PriorityClassTests)
	Describe("ConfigMap Syncer", ConfigMapTests)
	Describe("Secret Syncer", SecretTests)
	Describe("Service Syncer", ServiceTests)
	Describe("Ingress Syncer", IngressTests)
	Describe("PersistentVolumeClaim Syncer", PVCTests)
})

func translateName(cluster v1alpha1.Cluster, namespace, name string) string {
	translator := translate.ToHostTranslator{
		ClusterName:      cluster.Name,
		ClusterNamespace: cluster.Namespace,
	}

	return translator.TranslateName(namespace, name)
}
