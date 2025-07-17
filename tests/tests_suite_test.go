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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"go.uber.org/zap"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tests Suite")
}

var (
	k3sContainer   *k3s.K3sContainer
	hostIP         string
	restcfg        *rest.Config
	k8s            *kubernetes.Clientset
	k8sClient      client.Client
	kubeconfigPath string
)

var _ = BeforeSuite(func() {
	var err error
	ctx := context.Background()

	k3sContainer, err = k3s.Run(ctx, "rancher/k3s:v1.32.1-k3s1")
	Expect(err).To(Not(HaveOccurred()))

	hostIP, err = k3sContainer.ContainerIP(ctx)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Println("K3s containerIP: " + hostIP)

	kubeconfig, err := k3sContainer.GetKubeConfig(context.Background())
	Expect(err).To(Not(HaveOccurred()))

	tmpFile, err := os.CreateTemp("", "kubeconfig-")
	Expect(err).To(Not(HaveOccurred()))
	defer tmpFile.Close()

	_, err = tmpFile.Write(kubeconfig)
	Expect(err).To(Not(HaveOccurred()))
	kubeconfigPath = tmpFile.Name()
	os.Setenv("KUBECONFIG", kubeconfigPath)

	DeferCleanup(os.Remove, kubeconfigPath)

	initKubernetesClient(kubeconfig)
	installK3kChart(kubeconfig)
})

func initKubernetesClient(kubeconfig []byte) {
	var err error

	restcfg, err = clientcmd.RESTConfigFromKubeConfig(kubeconfig)
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

	err = actionConfig.Init(restClientGetter, releaseNamespace, os.Getenv("HELM_DRIVER"), func(format string, v ...any) {
		GinkgoWriter.Printf("helm debug: "+format+"\n", v...)
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

	GinkgoWriter.Printf("Release %s installed in %s namespace\n", release.Name, release.Namespace)
}

var _ = AfterSuite(func() {
	// dump k3s logs
	readCloser, err := k3sContainer.Logs(context.Background())
	Expect(err).To(Not(HaveOccurred()))

	logs, err := io.ReadAll(readCloser)
	Expect(err).To(Not(HaveOccurred()))

	logfile := path.Join(os.TempDir(), "k3s.log")
	err = os.WriteFile(logfile, logs, 0o644)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Println("k3s logs written to: " + logfile)

	// dump k3k controller logs
	readCloser, err = k3sContainer.Logs(context.Background())
	Expect(err).To(Not(HaveOccurred()))
	writeLogs("k3s.log", readCloser)

	// dump k3k logs
	writeK3kLogs()

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

func writeK3kLogs() {
	var (
		err     error
		podList corev1.PodList
	)

	ctx := context.Background()
	err = k8sClient.List(ctx, &podList, &client.ListOptions{Namespace: "k3k-system"})
	Expect(err).To(Not(HaveOccurred()))

	k3kPod := podList.Items[0]
	req := k8s.CoreV1().Pods(k3kPod.Namespace).GetLogs(k3kPod.Name, &corev1.PodLogOptions{})
	podLogs, err := req.Stream(ctx)
	Expect(err).To(Not(HaveOccurred()))
	writeLogs("k3k.log", podLogs)
}

func writeLogs(filename string, logs io.ReadCloser) {
	logsStr, err := io.ReadAll(logs)
	Expect(err).To(Not(HaveOccurred()))

	tempfile := path.Join(os.TempDir(), filename)
	err = os.WriteFile(tempfile, []byte(logsStr), 0o644)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Println("logs written to: " + filename)

	_ = logs.Close()
}

func readFileWithinPod(ctx context.Context, client *kubernetes.Clientset, config *rest.Config, name, namespace, path string) ([]byte, error) {
	command := []string{"cat", path}

	output := new(bytes.Buffer)

	stderr, err := podExec(ctx, client, config, namespace, name, command, nil, output)
	if err != nil || len(stderr) > 0 {
		return nil, fmt.Errorf("faile to read the following file %s: %v", path, err)
	}

	return output.Bytes(), nil
}

func podExec(ctx context.Context, clientset *kubernetes.Clientset, config *rest.Config, namespace, name string, command []string, stdin io.Reader, stdout io.Writer) ([]byte, error) {
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(name).
		Namespace(namespace).
		SubResource("exec")
	scheme := runtime.NewScheme()

	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("error adding to scheme: %v", err)
	}

	parameterCodec := runtime.NewParameterCodec(scheme)

	req.VersionedParams(&corev1.PodExecOptions{
		Command: command,
		Stdin:   stdin != nil,
		Stdout:  stdout != nil,
		Stderr:  true,
		TTY:     false,
	}, parameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("error while creating Executor: %v", err)
	}

	var stderr bytes.Buffer

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: &stderr,
		Tty:    false,
	})
	if err != nil {
		return nil, fmt.Errorf("error in Stream: %v", err)
	}

	return stderr.Bytes(), nil
}

func caCertSecret(name, namespace string, crt, key []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		Data: map[string][]byte{
			"tls.crt": crt,
			"tls.key": key,
		},
	}
}
