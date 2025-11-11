package cluster_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster Controller Suite")
}

var (
	testEnv   *envtest.Environment
	k8s       *kubernetes.Clientset
	k8sClient client.Client
	ctx       context.Context
	cancel    context.CancelFunc
)

var _ = BeforeSuite(func() {
	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "charts", "k3k", "templates", "crds")},
		ErrorIfCRDPathMissing: true,
	}

	// setting controller namespace env to activate port range allocator
	_ = os.Setenv("CONTROLLER_NAMESPACE", "default")

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	k8s, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())

	scheme := buildScheme()
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	ctrl.SetLogger(zapr.NewLogger(zap.NewNop()))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	portAllocator, err := agent.NewPortAllocator(ctx, mgr.GetClient())
	Expect(err).NotTo(HaveOccurred())

	err = mgr.Add(portAllocator.InitPortAllocatorConfig(ctx, mgr.GetClient(), "50000-51000", "51001-52000"))
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel = context.WithCancel(context.Background())

	clusterConfig := &cluster.Config{
		SharedAgentImage:  "rancher/k3k-kubelet:latest",
		K3SServerImage:    "rancher/k3s",
		VirtualAgentImage: "rancher/k3s",
	}
	err = cluster.Add(ctx, mgr, clusterConfig, 50, portAllocator, &record.FakeRecorder{})
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()

	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func buildScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	err := clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = v1beta1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	return scheme
}
