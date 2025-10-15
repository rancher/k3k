package k3k_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
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
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubernetes/pkg/api/v1/pod"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	k3kNamespace = "k3k-system"
	k3kName      = "k3k"

	slowTestsLabel         = "slow"
	updateTestsLabel       = "update"
	persistenceTestsLabel  = "persistence"
	networkingTestsLabel   = "networking"
	statusTestsLabel       = "status"
	certificatesTestsLabel = "certificates"
)

func TestTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tests Suite")
}

var (
	k3sContainer     *k3s.K3sContainer
	hostIP           string
	restcfg          *rest.Config
	k8s              *kubernetes.Clientset
	k8sClient        client.Client
	kubeconfigPath   string
	repo             string
	helmActionConfig *action.Configuration
)

var _ = BeforeSuite(func() {
	ctx := context.Background()

	GinkgoWriter.Println("GOCOVERDIR:", os.Getenv("GOCOVERDIR"))

	repo = os.Getenv("REPO")
	if repo == "" {
		repo = "rancher"
	}

	_, dockerInstallEnabled := os.LookupEnv("K3K_DOCKER_INSTALL")

	if dockerInstallEnabled {
		installK3SDocker(ctx)
		initKubernetesClient(ctx)
		installK3kChart()
	} else {
		initKubernetesClient(ctx)
	}

	patchPVC(ctx, k8s)
})

func initKubernetesClient(ctx context.Context) {
	var (
		err        error
		kubeconfig []byte
	)

	kubeconfigPath := os.Getenv("KUBECONFIG")
	Expect(kubeconfigPath).To(Not(BeEmpty()))

	kubeconfig, err = os.ReadFile(kubeconfigPath)
	Expect(err).To(Not(HaveOccurred()))

	restcfg, err = clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	Expect(err).To(Not(HaveOccurred()))

	hostIP, err = getServerIP(ctx, restcfg)
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

func buildScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	err := v1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = v1beta1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	return scheme
}

func installK3SDocker(ctx context.Context) {
	var (
		err        error
		kubeconfig []byte
	)

	k3sHostVersion := os.Getenv("K3S_HOST_VERSION")
	if k3sHostVersion == "" {
		k3sHostVersion = "v1.32.1+k3s1"
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

	err = k3sContainer.LoadImages(ctx, repo+"/k3k:dev", repo+"/k3k-kubelet:dev")
	Expect(err).To(Not(HaveOccurred()))
	DeferCleanup(os.Remove, kubeconfigPath)

	Expect(os.Setenv("KUBECONFIG", kubeconfigPath)).To(Succeed())
	GinkgoWriter.Print(kubeconfigPath)
	GinkgoWriter.Print(string(kubeconfig))
}

func installK3kChart() {
	pwd, err := os.Getwd()
	Expect(err).To(Not(HaveOccurred()))

	k3kChart, err := loader.Load(path.Join(pwd, "../charts/k3k"))
	Expect(err).To(Not(HaveOccurred()))

	helmActionConfig = new(action.Configuration)

	kubeconfig, err := os.ReadFile(kubeconfigPath)
	Expect(err).To(Not(HaveOccurred()))

	restClientGetter, err := NewRESTClientGetter(kubeconfig)
	Expect(err).To(Not(HaveOccurred()))

	err = helmActionConfig.Init(restClientGetter, k3kNamespace, os.Getenv("HELM_DRIVER"), func(format string, v ...any) {
		GinkgoWriter.Printf("helm debug: "+format+"\n", v...)
	})
	Expect(err).To(Not(HaveOccurred()))

	iCli := action.NewInstall(helmActionConfig)
	iCli.ReleaseName = k3kName
	iCli.Namespace = k3kNamespace
	iCli.CreateNamespace = true
	iCli.Timeout = time.Minute
	iCli.Wait = true

	controllerMap, _ := k3kChart.Values["controller"].(map[string]any)
	imageMap, _ := controllerMap["image"].(map[string]any)
	maps.Copy(imageMap, map[string]any{
		"repository": repo + "/k3k",
		"tag":        "dev",
		"pullPolicy": "IfNotPresent",
	})

	agentMap, _ := k3kChart.Values["agent"].(map[string]any)
	sharedAgentMap, _ := agentMap["shared"].(map[string]any)
	sharedAgentImageMap, _ := sharedAgentMap["image"].(map[string]any)
	maps.Copy(sharedAgentImageMap, map[string]any{
		"repository": repo + "/k3k-kubelet",
		"tag":        "dev",
	})

	release, err := iCli.Run(k3kChart, k3kChart.Values)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Printf("Release %s installed in %s namespace\n", release.Name, release.Namespace)
}

func patchPVC(ctx context.Context, clientset *kubernetes.Clientset) {
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coverage-data-pvc",
			Namespace: k3kNamespace,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceStorage: resource.MustParse("100M"),
				},
			},
		},
	}

	_, err := clientset.CoreV1().PersistentVolumeClaims(k3kNamespace).Create(ctx, pvc, metav1.CreateOptions{})
	Expect(client.IgnoreAlreadyExists(err)).To(Not(HaveOccurred()))

	patchData := []byte(`
{
    "spec": {
        "template": {
            "spec": {
                "volumes": [
                    {
                        "name": "tmp-covdata",
                        "persistentVolumeClaim": {
                            "claimName": "coverage-data-pvc"
                        }
                    }
                ],
                "containers": [
                    {
                        "name": "k3k",
                        "volumeMounts": [
                            {
                                "name": "tmp-covdata",
                                "mountPath": "/tmp/covdata"
                            }
                        ],
                        "env": [
                            {
                                "name": "GOCOVERDIR",
                                "value": "/tmp/covdata"
                            }
                        ]
                    }
                ]
            }
        }
    }
}`)

	GinkgoWriter.Printf("Applying patch to deployment '%s' in namespace '%s'...\n", k3kName, k3kNamespace)

	_, err = clientset.AppsV1().Deployments(k3kNamespace).Patch(
		ctx,
		k3kName,
		types.StrategicMergePatchType,
		patchData,
		metav1.PatchOptions{},
	)
	Expect(err).To(Not(HaveOccurred()))

	Eventually(func() bool {
		GinkgoWriter.Println("Checking K3k deployment status")

		dep, err := clientset.AppsV1().Deployments(k3kNamespace).Get(ctx, k3kName, metav1.GetOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// 1. Check if the controller has observed the latest generation
		if dep.Generation > dep.Status.ObservedGeneration {
			GinkgoWriter.Printf("K3k deployment generation: %d, observed generation: %d\n", dep.Generation, dep.Status.ObservedGeneration)
			return false
		}

		// 2. Check if all replicas have been updated
		if dep.Spec.Replicas != nil && dep.Status.UpdatedReplicas < *dep.Spec.Replicas {
			GinkgoWriter.Printf("K3k deployment replicas: %d, updated replicas: %d\n", *dep.Spec.Replicas, dep.Status.UpdatedReplicas)
			return false
		}

		// 3. Check if all updated replicas are available
		if dep.Status.AvailableReplicas < dep.Status.UpdatedReplicas {
			GinkgoWriter.Printf("K3k deployment availabl replicas: %d, updated replicas: %d\n", dep.Status.AvailableReplicas, dep.Status.UpdatedReplicas)
			return false
		}

		return true
	}).
		MustPassRepeatedly(5).
		WithPolling(time.Second).
		WithTimeout(time.Second * 30).
		Should(BeTrue())
}

var _ = AfterSuite(func() {
	ctx := context.Background()

	goCoverDir := os.Getenv("GOCOVERDIR")
	if goCoverDir == "" {
		goCoverDir = path.Join(os.TempDir(), "covdata")
		Expect(os.MkdirAll(goCoverDir, 0o755)).To(Succeed())
	}

	dumpK3kCoverageData(ctx, goCoverDir)
	if k3sContainer != nil {
		// dump k3s logs
		k3sLogs, err := k3sContainer.Logs(ctx)
		Expect(err).To(Not(HaveOccurred()))
		writeLogs("k3s.log", k3sLogs)

		// dump k3k controller logs
		k3kLogs := getK3kLogs(ctx)
		writeLogs("k3k.log", k3kLogs)

		testcontainers.CleanupContainer(GinkgoTB(), k3sContainer)
	}
})

// dumpK3kCoverageData will kill the K3k controller container to force it to dump the coverage data.
// It will then download the files with kubectl cp into the specified folder. If the folder doesn't exists it will be created.
func dumpK3kCoverageData(ctx context.Context, folder string) {
	By("Restarting k3k controller")

	var podList v1.PodList

	err := k8sClient.List(ctx, &podList, &client.ListOptions{Namespace: k3kNamespace})
	Expect(err).To(Not(HaveOccurred()))

	k3kPod := podList.Items[0]

	cmd := exec.Command("kubectl", "exec", "-n", k3kNamespace, k3kPod.Name, "-c", "k3k", "--", "kill", "1")
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))

	By("Waiting to be ready again")

	Eventually(func(g Gomega) {
		key := types.NamespacedName{
			Namespace: k3kNamespace,
			Name:      k3kPod.Name,
		}

		var controllerPod v1.Pod

		err = k8sClient.Get(ctx, key, &controllerPod)
		g.Expect(err).To(Not(HaveOccurred()))

		_, cond := pod.GetPodCondition(&controllerPod.Status, v1.PodReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
	}).
		MustPassRepeatedly(5).
		WithPolling(time.Second * 2).
		WithTimeout(time.Minute * 2).
		Should(Succeed())

	By("Controller is ready again, dumping coverage data")

	GinkgoWriter.Printf("Downloading covdata from k3k controller %s/%s to %s\n", k3kNamespace, k3kPod.Name, folder)

	cmd = exec.Command("kubectl", "cp", fmt.Sprintf("%s/%s:/tmp/covdata", k3kNamespace, k3kPod.Name), folder)
	output, err = cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
}

func getK3kLogs(ctx context.Context) io.ReadCloser {
	var podList v1.PodList

	err := k8sClient.List(ctx, &podList, &client.ListOptions{Namespace: k3kNamespace})
	Expect(err).To(Not(HaveOccurred()))

	k3kPod := podList.Items[0]
	req := k8s.CoreV1().Pods(k3kPod.Namespace).GetLogs(k3kPod.Name, &v1.PodLogOptions{Previous: true})
	podLogs, err := req.Stream(ctx)
	Expect(err).To(Not(HaveOccurred()))

	return podLogs
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
		return nil, fmt.Errorf("failed to read the following file %s: %v", path, err)
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

	if err := v1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("error adding to scheme: %v", err)
	}

	parameterCodec := runtime.NewParameterCodec(scheme)

	req.VersionedParams(&v1.PodExecOptions{
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

func caCertSecret(name, namespace string, crt, key []byte) *v1.Secret {
	return &v1.Secret{
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
