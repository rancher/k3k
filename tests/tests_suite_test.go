package k3k_test

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"go.uber.org/zap"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tests Suite")
}

var (
	k3sContainer *k3s.K3sContainer
	k8s          *kubernetes.Clientset
	k8sClient    client.Client
)

var _ = BeforeSuite(func() {
	var err error
	ctx := context.Background()

	k3sContainer, err = k3s.Run(ctx, "rancher/k3s:v1.32.1-k3s1")
	Expect(err).To(Not(HaveOccurred()))

	kubeconfig, err := k3sContainer.GetKubeConfig(context.Background())
	Expect(err).To(Not(HaveOccurred()))

	initKubernetesClient(kubeconfig)
	installK3kChart(kubeconfig)
})

func initKubernetesClient(kubeconfig []byte) {
	restcfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	Expect(err).To(Not(HaveOccurred()))

	k8s, err = kubernetes.NewForConfig(restcfg)
	Expect(err).To(Not(HaveOccurred()))

	scheme := buildScheme()
	k8sClient, err = client.New(restcfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	logger, err := zap.NewDevelopment()
	Expect(err).NotTo(HaveOccurred())
	log.SetLogger(zapr.NewLogger(logger))
}

func installK3kChart(kubeconfig []byte) {
	pwd, err := os.Getwd()
	Expect(err).To(Not(HaveOccurred()))

	k3kChart, err := loader.Load(path.Join(pwd, "../charts/k3k"))
	Expect(err).To(Not(HaveOccurred()))

	actionConfig := new(action.Configuration)

	restClientGetter, err := NewRESTClientGetter(kubeconfig)
	Expect(err).To(Not(HaveOccurred()))

	releaseName := "k3k"
	releaseNamespace := "k3k-system"

	err = actionConfig.Init(restClientGetter, releaseNamespace, os.Getenv("HELM_DRIVER"), func(format string, v ...interface{}) {
		fmt.Fprintf(GinkgoWriter, "helm debug: "+format+"\n", v...)
	})
	Expect(err).To(Not(HaveOccurred()))

	iCli := action.NewInstall(actionConfig)
	iCli.ReleaseName = releaseName
	iCli.Namespace = releaseNamespace
	iCli.CreateNamespace = true
	iCli.Timeout = time.Minute
	iCli.Wait = true

	imageMap, _ := k3kChart.Values["image"].(map[string]any)
	maps.Copy(imageMap, map[string]any{
		"repository": "rancher/k3k",
		"tag":        "dev",
		"pullPolicy": "IfNotPresent",
	})

	sharedAgentMap, _ := k3kChart.Values["sharedAgent"].(map[string]any)
	sharedAgentImageMap, _ := sharedAgentMap["image"].(map[string]any)
	maps.Copy(sharedAgentImageMap, map[string]any{
		"repository": "rancher/k3k-kubelet",
		"tag":        "dev",
	})

	err = k3sContainer.LoadImages(context.Background(), "rancher/k3k:dev", "rancher/k3k-kubelet:dev")
	Expect(err).To(Not(HaveOccurred()))

	release, err := iCli.Run(k3kChart, k3kChart.Values)
	Expect(err).To(Not(HaveOccurred()))

	fmt.Fprintf(GinkgoWriter, "Release %s installed in %s namespace\n", release.Name, release.Namespace)
}

var _ = AfterSuite(func() {
	// dump k3s logs
	readCloser, err := k3sContainer.Logs(context.Background())
	Expect(err).To(Not(HaveOccurred()))

	logs, err := io.ReadAll(readCloser)
	Expect(err).To(Not(HaveOccurred()))

	logfile := path.Join(os.TempDir(), "k3s.log")
	err = os.WriteFile(logfile, logs, 0644)
	Expect(err).To(Not(HaveOccurred()))

	fmt.Fprintln(GinkgoWriter, "k3s logs written to: "+logfile)

	testcontainers.CleanupContainer(GinkgoTB(), k3sContainer)
})

var _ = When("k3k is installed", func() {
	It("has to be in a Ready state", func() {

		// check that at least a Pod is in Ready state
		Eventually(func() bool {
			opts := v1.ListOptions{LabelSelector: "app.kubernetes.io/name=k3k"}
			podList, err := k8s.CoreV1().Pods("k3k-system").List(context.Background(), opts)

			Expect(err).To(Not(HaveOccurred()))
			Expect(podList.Items).To(Not(BeEmpty()))

			isReady := false

		outer:
			for _, pod := range podList.Items {
				for _, condition := range pod.Status.Conditions {
					if condition.Status != corev1.ConditionTrue {
						continue
					}

					if condition.Type == corev1.PodReady {
						isReady = true
						break outer
					}
				}
			}

			return isReady
		}).
			WithTimeout(time.Second * 10).
			WithPolling(time.Second).
			Should(BeTrue())
	})
})

func buildScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	err := corev1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = v1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	return scheme
}
