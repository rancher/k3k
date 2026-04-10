package policy_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/rancher/k3k/pkg/controller/policy"
	fwclient "github.com/rancher/k3k/tests/framework/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VirtualClusterPolicy Controller Suite")
}

var (
	testEnv   *envtest.Environment
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
	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	scheme := fwclient.NewScheme()
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}})
	Expect(err).NotTo(HaveOccurred())

	ctrl.SetLogger(zapr.NewLogger(zap.NewNop()))

	ctx, cancel = context.WithCancel(context.Background())
	err = policy.Add(mgr, "", 50)
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
