package cli_test

import (
	"context"
	"io"
	"maps"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"go.uber.org/zap"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

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
	k3sContainer     *k3s.K3sContainer
	restcfg          *rest.Config
	k8s              *kubernetes.Clientset
	k8sClient        client.Client
	kubeconfigPath   string
	helmActionConfig *action.Configuration
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
		initKubernetesClient()
		installK3kChart(repo+"/k3k", repo+"/k3k-kubelet")
	} else {
		initKubernetesClient()
	}
})

func initKubernetesClient() {
	var (
		err        error
		kubeconfig []byte
	)

	logger, err := zap.NewDevelopment()
	Expect(err).NotTo(HaveOccurred())

	log.SetLogger(zapr.NewLogger(logger))

	kubeconfigPath := os.Getenv("KUBECONFIG")
	Expect(kubeconfigPath).To(Not(BeEmpty()))

	kubeconfig, err = os.ReadFile(kubeconfigPath)
	Expect(err).To(Not(HaveOccurred()))

	restcfg, err = clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	Expect(err).To(Not(HaveOccurred()))

	k8s, err = kubernetes.NewForConfig(restcfg)
	Expect(err).To(Not(HaveOccurred()))

	scheme := buildScheme()
	k8sClient, err = client.New(restcfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
}

func buildScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	err := clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = v1beta1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	return scheme
}

func installK3SDocker(ctx context.Context, controllerImage, kubeletImage string) {
	var (
		err        error
		kubeconfig []byte
	)

	k3sHostVersion := os.Getenv("K3S_HOST_VERSION")
	if k3sHostVersion == "" {
		k3sHostVersion = k3sVersion
	}

	k3sHostVersion = strings.ReplaceAll(k3sHostVersion, "+", "-")

	k3sContainer, err = k3s.Run(ctx, "rancher/k3s:"+k3sHostVersion)
	Expect(err).To(Not(HaveOccurred()))

	containerIP, err := k3sContainer.ContainerIP(ctx)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Println("K3s containerIP: " + containerIP)

	kubeconfig, err = k3sContainer.GetKubeConfig(context.Background())
	Expect(err).To(Not(HaveOccurred()))

	tmpFile, err := os.CreateTemp("", "kubeconfig-")
	Expect(err).To(Not(HaveOccurred()))

	_, err = tmpFile.Write(kubeconfig)
	Expect(err).To(Not(HaveOccurred()))
	Expect(tmpFile.Close()).To(Succeed())
	kubeconfigPath = tmpFile.Name()

	err = k3sContainer.LoadImages(ctx, controllerImage+":dev", kubeletImage+":dev")
	Expect(err).To(Not(HaveOccurred()))
	DeferCleanup(os.Remove, kubeconfigPath)

	Expect(os.Setenv("KUBECONFIG", kubeconfigPath)).To(Succeed())
	GinkgoWriter.Printf("KUBECONFIG set to: %s\n", kubeconfigPath)
}

func installK3kChart(controllerImage, kubeletImage string) {
	pwd, err := os.Getwd()
	Expect(err).To(Not(HaveOccurred()))

	k3kChart, err := loader.Load(path.Join(pwd, "../../charts/k3k"))
	Expect(err).To(Not(HaveOccurred()))

	helmActionConfig = new(action.Configuration)

	kubeconfig, err := os.ReadFile(kubeconfigPath)
	Expect(err).To(Not(HaveOccurred()))

	restClientGetter, err := NewRESTClientGetter(kubeconfig)
	Expect(err).To(Not(HaveOccurred()))

	err = helmActionConfig.Init(restClientGetter, k3kNamespace, os.Getenv("HELM_DRIVER"), func(format string, v ...any) {
		GinkgoWriter.Printf("[Helm] "+format+"\n", v...)
	})
	Expect(err).To(Not(HaveOccurred()))

	iCli := action.NewInstall(helmActionConfig)
	iCli.ReleaseName = "k3k"
	iCli.Namespace = k3kNamespace
	iCli.CreateNamespace = true
	iCli.Timeout = time.Minute
	iCli.Wait = true

	controllerMap, _ := k3kChart.Values["controller"].(map[string]any)

	extraEnvArray, _ := controllerMap["extraEnv"].([]map[string]any)
	extraEnvArray = append(extraEnvArray, map[string]any{
		"name":  "DEBUG",
		"value": "true",
	})
	controllerMap["extraEnv"] = extraEnvArray

	imageMap, _ := controllerMap["image"].(map[string]any)
	maps.Copy(imageMap, map[string]any{
		"repository": controllerImage,
		"tag":        "dev",
		"pullPolicy": "IfNotPresent",
	})

	agentMap, _ := k3kChart.Values["agent"].(map[string]any)
	sharedAgentMap, _ := agentMap["shared"].(map[string]any)
	sharedAgentImageMap, _ := sharedAgentMap["image"].(map[string]any)
	maps.Copy(sharedAgentImageMap, map[string]any{
		"repository": kubeletImage,
		"tag":        "dev",
	})

	release, err := iCli.Run(k3kChart, k3kChart.Values)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Printf("Helm release '%s' installed in '%s' namespace\n", release.Name, release.Namespace)
}

var _ = AfterSuite(func() {
	ctx := context.Background()

	if k3sContainer != nil {
		// dump k3s logs
		k3sLogs, err := k3sContainer.Logs(ctx)
		Expect(err).To(Not(HaveOccurred()))
		writeLogs("k3s.log", k3sLogs)

		testcontainers.CleanupContainer(GinkgoTB(), k3sContainer)
	}
})

func writeLogs(filename string, logs io.ReadCloser) {
	logsStr, err := io.ReadAll(logs)
	Expect(err).To(Not(HaveOccurred()))

	tempfile := path.Join(os.TempDir(), filename)
	err = os.WriteFile(tempfile, []byte(logsStr), 0o644)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Println("logs written to: " + filename)

	_ = logs.Close()
}
