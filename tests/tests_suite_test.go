package k3k_test

import (
	"bytes"
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
	v1 "k8s.io/api/core/v1"
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
	hostIP       string
	k8s          *kubernetes.Clientset
	k8sClient    client.Client
	kubecfg      []byte
)

var _ = BeforeSuite(func() {
	var err error
	ctx := context.Background()

	k3sContainer, err = k3s.Run(ctx, "rancher/k3s:v1.32.1-k3s1")
	Expect(err).To(Not(HaveOccurred()))

	hostIP, err = k3sContainer.ContainerIP(ctx)
	Expect(err).To(Not(HaveOccurred()))
	fmt.Fprintln(GinkgoWriter, "K3s containerIP: "+hostIP)

	kubecfg, err = k3sContainer.GetKubeConfig(context.Background())
	Expect(err).To(Not(HaveOccurred()))

	initKubernetesClient()
	installK3kChart()
})

func initKubernetesClient() {
	restcfg, err := clientcmd.RESTConfigFromKubeConfig(kubecfg)
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

func installK3kChart() {
	pwd, err := os.Getwd()
	Expect(err).To(Not(HaveOccurred()))

	k3kChart, err := loader.Load(path.Join(pwd, "../charts/k3k"))
	Expect(err).To(Not(HaveOccurred()))

	actionConfig := new(action.Configuration)

	restClientGetter, err := NewRESTClientGetter(kubecfg)
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

	// dump k3k controller logs
	var podList v1.PodList
	listOpts := &client.ListOptions{Namespace: "k3k-system"}
	err = k8sClient.List(context.Background(), &podList, listOpts)
	Expect(err).To(Not(HaveOccurred()))
	k3kLogFile := path.Join(os.TempDir(), "k3k.log")
	k3kLogs, err := getPodLogs(podList.Items[0])
	Expect(err).To(Not(HaveOccurred()))
	err = os.WriteFile(k3kLogFile, []byte(k3kLogs), 0644)
	Expect(err).To(Not(HaveOccurred()))

	fmt.Fprintln(GinkgoWriter, "k3k logs written to: "+k3kLogFile)

	testcontainers.CleanupContainer(GinkgoTB(), k3sContainer)
})

func buildScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	err := corev1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = v1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	return scheme
}

func getPodLogs(pod corev1.Pod) (string, error) {
	podLogOpts := corev1.PodLogOptions{}
	restcfg, err := clientcmd.RESTConfigFromKubeConfig(kubecfg)
	if err != nil {
		return "", err
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(restcfg)
	if err != nil {
		return "", err
	}
	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &podLogOpts)
	podLogs, err := req.Stream(context.Background())
	if err != nil {
		return "", err
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", err
	}
	str := buf.String()

	return str, nil
}
