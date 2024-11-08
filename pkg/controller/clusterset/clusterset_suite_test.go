package clusterset_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/clusterset"
	"github.com/rancher/k3k/pkg/log"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var (
	k8sClient client.Client
)

var _ = BeforeSuite(func() {
	ctx, _ := context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "charts", "k3k", "crds")},
		ErrorIfCRDPathMissing: true,
		CRDInstallOptions: envtest.CRDInstallOptions{
			MaxTime: 60 * time.Second,
		},
	}
	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme := runtime.NewScheme()
	err = v1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	err = clusterset.Add(ctx, mgr, "", log.New(true))
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = Describe("MemcachedController", func() {
	Context("testing memcache controller", func() {

		It("should create deployment", func() {
			clusterSet := &v1alpha1.ClusterSet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "default",
				},
				Spec: v1alpha1.ClusterSetSpec{
					MaxLimits: nil,
				},
			}

			err := k8sClient.Create(context.Background(), clusterSet)
			Expect(err).To(Not(HaveOccurred()))

			// look for network policies etc

			// createdDeploy := &appsv1.Deployment{}
			// deployKey := types.NamespacedName{Name: memcached.Name, Namespace: memcached.Namespace}
			// Eventually(func() bool {
			// 	err := k8sClient.Get(ctx, deployKey, createdDeploy)
			// 	return err == nil
			// }, time.Second*10, time.Millisecond*250).Should(BeTrue())
		})
	})
})

// var _ = AfterSuite(func() {
// 	cancel()
// 	By("tearing down the test environment")
// 	err := testEnv.Stop()
// 	Expect(err).NotTo(HaveOccurred())
// })
