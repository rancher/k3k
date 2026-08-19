package k3k_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
	"k8s.io/kubernetes/pkg/api/v1/pod"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type VirtualCluster struct {
	Cluster    *v1beta1.Cluster
	RestConfig *rest.Config
	Client     *kubernetes.Clientset
	Kubeconfig []byte
}

func NewVirtualCluster() *VirtualCluster { // By default, create an ephemeral cluster
	GinkgoHelper()

	return NewVirtualClusterWithType(v1beta1.EphemeralPersistenceMode)
}

func NewVirtualClusterWithType(persistenceType v1beta1.PersistenceMode) *VirtualCluster {
	GinkgoHelper()

	namespace := fw.CreateNamespace()

	cluster := NewCluster(namespace.Name)
	cluster.Spec.Persistence.Type = persistenceType

	CreateCluster(cluster)

	client, restConfig, kubeconfig := NewVirtualK8sClientAndKubeconfig(cluster)

	By(fmt.Sprintf("Created virtual cluster %s/%s", cluster.Namespace, cluster.Name))

	return &VirtualCluster{
		Cluster:    cluster,
		RestConfig: restConfig,
		Client:     client,
		Kubeconfig: kubeconfig,
	}
}

// NewVirtualClusters will create multiple Virtual Clusters asynchronously
func NewVirtualClusters(n int) []*VirtualCluster {
	GinkgoHelper()

	clusters := make([]*VirtualCluster, n)

	wg := sync.WaitGroup{}
	wg.Add(n)

	for i := range n {
		go func() {
			defer wg.Done()
			defer GinkgoRecover()

			clusters[i] = NewVirtualCluster()
		}()
	}

	wg.Wait()

	return clusters
}

func NewCluster(namespace string, opts ...func(*v1beta1.Cluster)) *v1beta1.Cluster {
	return fw.NewCluster(namespace, opts...)
}

func CreateCluster(cluster *v1beta1.Cluster) {
	GinkgoHelper()

	fw.CreateCluster(context.Background(), cluster)
}

// NewVirtualK8sClient returns a Kubernetes ClientSet for the virtual cluster
func NewVirtualK8sClient(cluster *v1beta1.Cluster) *kubernetes.Clientset {
	GinkgoHelper()

	virtualK8sClient, _, _ := fw.NewVirtualK8sClient(context.Background(), cluster)

	return virtualK8sClient
}

// NewVirtualK8sClientAndConfig returns a Kubernetes ClientSet for the virtual cluster, and its rest.Config
func NewVirtualK8sClientAndConfig(cluster *v1beta1.Cluster) (*kubernetes.Clientset, *rest.Config) {
	GinkgoHelper()

	virtualK8sClient, restcfg, _ := fw.NewVirtualK8sClient(context.Background(), cluster)

	return virtualK8sClient, restcfg
}

// NewVirtualK8sClientAndKubeconfig returns a Kubernetes ClientSet for the virtual cluster, its rest.Config and its raw kubeconfig
func NewVirtualK8sClientAndKubeconfig(cluster *v1beta1.Cluster) (*kubernetes.Clientset, *rest.Config, []byte) {
	GinkgoHelper()

	return fw.NewVirtualK8sClient(context.Background(), cluster)
}

func (c *VirtualCluster) NewNginxPod(namespace string) (*corev1.Pod, string) {
	GinkgoHelper()

	if namespace == "" {
		namespace = "default"
	}

	nginxPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "nginx-",
			Namespace:    namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "nginx",
				Image: "nginx",
			}},
		},
	}

	By("Creating Nginx Pod and waiting for it to be Ready")

	ctx := context.Background()

	var err error

	nginxPod, err = c.Client.CoreV1().Pods(nginxPod.Namespace).Create(ctx, nginxPod, metav1.CreateOptions{})
	Expect(err).To(Not(HaveOccurred()))

	// check that the nginx Pod is up and running in the virtual cluster
	Eventually(func(g Gomega) {
		nginxPod, err = c.Client.CoreV1().Pods(nginxPod.Namespace).Get(ctx, nginxPod.Name, metav1.GetOptions{})
		g.Expect(err).To(Not(HaveOccurred()))

		_, cond := pod.GetPodCondition(&nginxPod.Status, corev1.PodReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
	}).
		WithTimeout(time.Minute).
		WithPolling(time.Second).
		Should(Succeed())

	By(fmt.Sprintf("Nginx Pod is running (%s/%s)", nginxPod.Namespace, nginxPod.Name))

	// only check the pod on the host cluster if the mode is shared mode.
	// hcp is agentless and BYO-node, so no host-side pod mirror exists.
	if c.Cluster.Spec.Mode != v1beta1.SharedClusterMode {
		return nginxPod, ""
	}

	var podIP string

	// check that the nginx Pod is up and running in the host cluster
	Eventually(func() bool {
		podList, err := k8s.CoreV1().Pods(c.Cluster.Namespace).List(ctx, metav1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))

		for _, pod := range podList.Items {
			resourceName := pod.Annotations[translate.ResourceNameAnnotation]
			resourceNamespace := pod.Annotations[translate.ResourceNamespaceAnnotation]

			if resourceName == nginxPod.Name && resourceNamespace == nginxPod.Namespace {
				podIP = pod.Status.PodIP

				GinkgoWriter.Printf(
					"pod=%s resource=%s/%s status=%s podIP=%s\n",
					pod.Name, resourceNamespace, resourceName, pod.Status.Phase, podIP,
				)

				return pod.Status.Phase == corev1.PodRunning && podIP != ""
			}
		}

		return false
	}).
		WithTimeout(time.Minute).
		WithPolling(time.Second * 5).
		Should(BeTrue())

	return nginxPod, podIP
}

// ExecCmd exec command on specific pod and wait the command's output.
func (c *VirtualCluster) ExecCmd(pod *corev1.Pod, command string) (string, string, error) {
	option := &corev1.PodExecOptions{
		Command: []string{"sh", "-c", command},
		Stdout:  true,
		Stderr:  true,
	}

	req := c.Client.CoreV1().RESTClient().Post().Resource("pods").Name(pod.Name).Namespace(pod.Namespace).SubResource("exec")
	req.VersionedParams(option, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.RestConfig, "POST", req.URL())
	if err != nil {
		return "", "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	})

	return stdout.String(), stderr.String(), err
}

func restartServerPod(ctx context.Context, virtualCluster *VirtualCluster) {
	GinkgoHelper()

	serverPods := listServerPods(ctx, virtualCluster)

	Expect(len(serverPods)).To(Equal(1))
	serverPod := serverPods[0]

	GinkgoWriter.Printf("deleting pod %s/%s\n", serverPod.Namespace, serverPod.Name)

	err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).Delete(ctx, serverPod.Name, metav1.DeleteOptions{})
	Expect(err).To(Not(HaveOccurred()))

	By("Deleting server pod")

	// check that the server pods restarted
	Eventually(func(g Gomega) {
		serverPods := listServerPods(ctx, virtualCluster)

		g.Expect(serverPods).To(HaveLen(1))
		g.Expect(serverPods[0].DeletionTimestamp).To(Not(BeNil()))
	}).
		WithTimeout(time.Minute * 2).
		WithPolling(time.Second * 5).
		Should(Succeed())
}

func listServerPods(ctx context.Context, virtualCluster *VirtualCluster) []corev1.Pod {
	GinkgoHelper()

	return fw.ListServerPods(ctx, virtualCluster.Cluster)
}

func listAgentPods(ctx context.Context, virtualCluster *VirtualCluster) []corev1.Pod {
	GinkgoHelper()

	return fw.ListAgentPods(ctx, virtualCluster.Cluster)
}

// getEnv will get an environment variable from a pod it will return empty string if not found
func getEnv(pod *corev1.Pod, envName string) (string, bool) {
	container := pod.Spec.Containers[0]
	for _, envVar := range container.Env {
		if envVar.Name == envName {
			return envVar.Value, true
		}
	}

	return "", false
}

// isArgFound will return true if the argument passed to the function is found in container args
func isArgFound(pod *corev1.Pod, arg string) bool {
	container := pod.Spec.Containers[0]
	for _, cmd := range container.Command {
		if strings.Contains(cmd, arg) {
			return true
		}
	}

	return false
}
