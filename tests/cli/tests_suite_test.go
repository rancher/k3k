package cli_test

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/tests/framework"

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
	fw        *framework.Framework
)

var _ = BeforeSuite(func() {
	ctx := context.Background()

	initKubernetesClient(ctx)
})

func initKubernetesClient(ctx context.Context) {
	var err error

	fw, err = framework.New(ctx)
	Expect(err).NotTo(HaveOccurred())

	restcfg = fw.RestConfig
	k8s = fw.Clientset
	k8sClient = fw.Client
}
