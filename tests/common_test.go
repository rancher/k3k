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
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type VirtualCluster struct {
	Cluster    *v1alpha1.Cluster
	RestConfig *rest.Config
	Client     *kubernetes.Clientset
}

func NewVirtualCluster() *VirtualCluster { // By default, create an ephemeral cluster
	GinkgoHelper()

	return NewVirtualClusterWithType(v1alpha1.EphemeralPersistenceMode)
}

func NewVirtualClusterWithType(persistenceType v1alpha1.PersistenceMode) *VirtualCluster {
	GinkgoHelper()

	namespace := NewNamespace()

	By(fmt.Sprintf("Creating new virtual cluster in namespace %s", namespace.Name))

	cluster := NewCluster(namespace.Name)
	cluster.Spec.Persistence.Type = persistenceType

	CreateCluster(cluster)

	client, restConfig := NewVirtualK8sClientAndConfig(cluster)

	By(fmt.Sprintf("Created virtual cluster %s/%s", cluster.Namespace, cluster.Name))

	return &VirtualCluster{
		Cluster:    cluster,
		RestConfig: restConfig,
		Client:     client,
	}
}

// NewVirtualClusters will create multiple Virtual Clusters asynchronously
func NewVirtualClusters(n int) []*VirtualCluster {
	GinkgoHelper()

	var clusters []*VirtualCluster

	wg := sync.WaitGroup{}
	wg.Add(n)

	for range n {
		go func() {
			defer wg.Done()
			defer GinkgoRecover()

			clusters = append(clusters, NewVirtualCluster())
		}()
	}

	wg.Wait()

	return clusters
}

func NewNamespace() *corev1.Namespace {
	GinkgoHelper()

	namespace := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{GenerateName: "ns-"}}
	namespace, err := k8s.CoreV1().Namespaces().Create(context.Background(), namespace, v1.CreateOptions{})
	Expect(err).To(Not(HaveOccurred()))

	return namespace
}

func DeleteNamespaces(names ...string) {
	GinkgoHelper()

	wg := sync.WaitGroup{}
	wg.Add(len(names))

	for _, name := range names {
		go func() {
			defer wg.Done()
			defer GinkgoRecover()

			deleteNamespace(name)
		}()
	}

	wg.Wait()
}

func deleteNamespace(name string) {
	GinkgoHelper()

	By(fmt.Sprintf("Deleting namespace %s", name))

	err := k8s.CoreV1().Namespaces().Delete(context.Background(), name, v1.DeleteOptions{
		GracePeriodSeconds: ptr.To[int64](0),
	})
	Expect(err).To(Not(HaveOccurred()))
}

func NewCluster(namespace string) *v1alpha1.Cluster {
	return &v1alpha1.Cluster{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: "cluster-",
			Namespace:    namespace,
		},
		Spec: v1alpha1.ClusterSpec{
			TLSSANs: []string{hostIP},
			Expose: &v1alpha1.ExposeConfig{
				NodePort: &v1alpha1.NodePortConfig{},
			},
			Persistence: v1alpha1.PersistenceConfig{
				Type: v1alpha1.EphemeralPersistenceMode,
			},
		},
	}
}

func CreateCluster(cluster *v1alpha1.Cluster) {
	GinkgoHelper()

	ctx := context.Background()
	err := k8sClient.Create(ctx, cluster)
	Expect(err).To(Not(HaveOccurred()))

	By("Waiting for cluster to be ready")

	// check that the server Pod and the Kubelet are in Ready state
	Eventually(func() bool {
		podList, err := k8s.CoreV1().Pods(cluster.Namespace).List(ctx, v1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// all the servers and agents needs to be in a running phase
		var serversReady, agentsReady int

		for _, pod := range podList.Items {
			if pod.Labels["role"] == "server" {
				GinkgoLogr.Info(fmt.Sprintf("server pod=%s/%s status=%s", pod.Namespace, pod.Name, pod.Status.Phase))
				if pod.Status.Phase == corev1.PodRunning {
					serversReady++
				}
			}

			if pod.Labels["type"] == "agent" {
				GinkgoLogr.Info(fmt.Sprintf("agent pod=%s/%s status=%s", pod.Namespace, pod.Name, pod.Status.Phase))
				if pod.Status.Phase == corev1.PodRunning {
					agentsReady++
				}
			}
		}

		expectedServers := int(*cluster.Spec.Servers)
		expectedAgents := int(*cluster.Spec.Agents)

		By(fmt.Sprintf("serversReady=%d/%d agentsReady=%d/%d", serversReady, expectedServers, agentsReady, expectedAgents))

		// the server pods should equal the expected servers, but since in shared mode we also have the kubelet is fine to have more than one
		if (serversReady != expectedServers) || (agentsReady < expectedAgents) {
			return false
		}

		return true
	}).
		WithTimeout(time.Minute * 5).
		WithPolling(time.Second * 5).
		Should(BeTrue())
}

// NewVirtualK8sClient returns a Kubernetes ClientSet for the virtual cluster
func NewVirtualK8sClient(cluster *v1alpha1.Cluster) *kubernetes.Clientset {
	virtualK8sClient, _ := NewVirtualK8sClientAndConfig(cluster)
	return virtualK8sClient
}

// NewVirtualK8sClient returns a Kubernetes ClientSet for the virtual cluster
func NewVirtualK8sClientAndConfig(cluster *v1alpha1.Cluster) (*kubernetes.Clientset, *rest.Config) {
	GinkgoHelper()

	var (
		err    error
		config *clientcmdapi.Config
	)

	ctx := context.Background()

	Eventually(func() error {
		vKubeconfig := kubeconfig.New()
		kubeletAltName := fmt.Sprintf("k3k-%s-kubelet", cluster.Name)
		vKubeconfig.AltNames = certs.AddSANs([]string{hostIP, kubeletAltName})
		config, err = vKubeconfig.Generate(ctx, k8sClient, cluster, hostIP, 0)
		return err
	}).
		WithTimeout(time.Minute * 2).
		WithPolling(time.Second * 5).
		Should(BeNil())

	configData, err := clientcmd.Write(*config)
	Expect(err).To(Not(HaveOccurred()))

	restcfg, err := clientcmd.RESTConfigFromKubeConfig(configData)
	Expect(err).To(Not(HaveOccurred()))
	virtualK8sClient, err := kubernetes.NewForConfig(restcfg)
	Expect(err).To(Not(HaveOccurred()))

	return virtualK8sClient, restcfg
}

func (c *VirtualCluster) NewNginxPod(namespace string) (*corev1.Pod, string) {
	GinkgoHelper()

	if namespace == "" {
		namespace = "default"
	}

	nginxPod := &corev1.Pod{
		ObjectMeta: v1.ObjectMeta{
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

	By("Creating Pod")

	ctx := context.Background()
	nginxPod, err := c.Client.CoreV1().Pods(nginxPod.Namespace).Create(ctx, nginxPod, v1.CreateOptions{})
	Expect(err).To(Not(HaveOccurred()))

	var podIP string

	// check that the nginx Pod is up and running in the host cluster
	Eventually(func() bool {
		podList, err := k8s.CoreV1().Pods(c.Cluster.Namespace).List(ctx, v1.ListOptions{})
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

	// get the running pod from the virtual cluster
	nginxPod, err = c.Client.CoreV1().Pods(nginxPod.Namespace).Get(ctx, nginxPod.Name, v1.GetOptions{})
	Expect(err).To(Not(HaveOccurred()))

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

	labelSelector := "cluster=" + virtualCluster.Cluster.Name + ",role=server"
	serverPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
	Expect(err).To(Not(HaveOccurred()))

	Expect(len(serverPods.Items)).To(Equal(1))
	serverPod := serverPods.Items[0]

	GinkgoWriter.Printf("deleting pod %s/%s\n", serverPod.Namespace, serverPod.Name)

	err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).Delete(ctx, serverPod.Name, v1.DeleteOptions{})
	Expect(err).To(Not(HaveOccurred()))

	By("Deleting server pod")

	// check that the server pods restarted
	Eventually(func() any {
		serverPods, err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))
		Expect(len(serverPods.Items)).To(Equal(1))
		return serverPods.Items[0].DeletionTimestamp
	}).WithTimeout(60 * time.Second).WithPolling(time.Second * 5).Should(BeNil())
}

func listServerPods(ctx context.Context, virtualCluster *VirtualCluster) []corev1.Pod {
	labelSelector := "cluster=" + virtualCluster.Cluster.Name + ",role=server"

	serverPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
	Expect(err).To(Not(HaveOccurred()))

	return serverPods.Items
}

func listAgentPods(ctx context.Context, virtualCluster *VirtualCluster) []corev1.Pod {
	labelSelector := fmt.Sprintf("cluster=%s,type=agent,mode=%s", virtualCluster.Cluster.Name, virtualCluster.Cluster.Spec.Mode)

	agentPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
	Expect(err).To(Not(HaveOccurred()))

	return agentPods.Items
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
