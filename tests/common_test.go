package k3k_test

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"

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

	namespace := NewNamespace()

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

func NewNamespace() *v1.Namespace {
	GinkgoHelper()

	namespace := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-", Labels: map[string]string{"e2e": "true"}}}
	namespace, err := k8s.CoreV1().Namespaces().Create(context.Background(), namespace, metav1.CreateOptions{})
	Expect(err).To(Not(HaveOccurred()))

	return namespace
}

func DeleteNamespaces(names ...string) {
	GinkgoHelper()

	if _, found := os.LookupEnv("KEEP_NAMESPACES"); found {
		By(fmt.Sprintf("Keeping namespace %v", names))
		return
	}

	wg := sync.WaitGroup{}
	wg.Add(len(names))

	for _, name := range names {
		go func() {
			defer wg.Done()
			defer GinkgoRecover()

			By(fmt.Sprintf("Deleting namespace %s", name))

			err := k8s.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{
				GracePeriodSeconds: ptr.To[int64](0),
			})
			Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))
		}()
	}

	wg.Wait()
}

func NewCluster(namespace string) *v1beta1.Cluster {
	return &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "cluster-",
			Namespace:    namespace,
		},
		Spec: v1beta1.ClusterSpec{
			TLSSANs: []string{hostIP},
			Expose: &v1beta1.ExposeConfig{
				NodePort: &v1beta1.NodePortConfig{},
			},
			Persistence: v1beta1.PersistenceConfig{
				Type: v1beta1.EphemeralPersistenceMode,
			},
		},
	}
}

func CreateCluster(cluster *v1beta1.Cluster) {
	GinkgoHelper()

	By(fmt.Sprintf("Creating new virtual cluster in namespace %s", cluster.Namespace))

	ctx := context.Background()
	err := k8sClient.Create(ctx, cluster)
	Expect(err).To(Not(HaveOccurred()))

	expectedServers := int(*cluster.Spec.Servers)
	expectedAgents := int(*cluster.Spec.Agents)

	By(fmt.Sprintf("Waiting for cluster %s to be ready in namespace %s. Expected servers: %d. Expected agents: %d", cluster.Name, cluster.Namespace, expectedServers, expectedAgents))

	// track the Eventually status to log for changes
	prev := -1

	// check that the server Pod and the Kubelet are in Ready state
	Eventually(func() bool {
		podList, err := k8s.CoreV1().Pods(cluster.Namespace).List(ctx, metav1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// all the servers and agents needs to be in a running phase
		var serversReady, agentsReady int

		for _, k3sPod := range podList.Items {
			_, cond := GetPodCondition(&k3sPod.Status, v1.PodReady)

			// pod not ready
			if cond == nil || cond.Status != v1.ConditionTrue {
				continue
			}

			if k3sPod.Labels["role"] == "server" {
				serversReady++
			}

			if k3sPod.Labels["type"] == "agent" {
				agentsReady++
			}
		}

		if prev != (serversReady + agentsReady) {
			GinkgoLogr.Info("Waiting for pods to be Ready",
				"servers", serversReady, "agents", agentsReady,
				"name", cluster.Name, "namespace", cluster.Namespace,
				"time", time.Now().Format(time.DateTime),
			)
			prev = (serversReady + agentsReady)
		}

		// the server pods should equal the expected servers, but since in shared mode we also have the kubelet is fine to have more than one
		if (serversReady != expectedServers) || (agentsReady < expectedAgents) {
			return false
		}

		return true
	}).
		WithTimeout(time.Minute * 5).
		WithPolling(time.Second * 10).
		Should(BeTrue())

	By("Cluster is ready")
}

// NewVirtualK8sClient returns a Kubernetes ClientSet for the virtual cluster
func NewVirtualK8sClient(cluster *v1beta1.Cluster) *kubernetes.Clientset {
	virtualK8sClient, _ := NewVirtualK8sClientAndConfig(cluster)
	return virtualK8sClient
}

// NewVirtualK8sClient returns a Kubernetes ClientSet for the virtual cluster
func NewVirtualK8sClientAndConfig(cluster *v1beta1.Cluster) (*kubernetes.Clientset, *rest.Config) {
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

// NewVirtualK8sClient returns a Kubernetes ClientSet for the virtual cluster
func NewVirtualK8sClientAndKubeconfig(cluster *v1beta1.Cluster) (*kubernetes.Clientset, *rest.Config, []byte) {
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

	return virtualK8sClient, restcfg, configData
}

func (c *VirtualCluster) NewNginxPod(namespace string) (*v1.Pod, string) {
	GinkgoHelper()

	if namespace == "" {
		namespace = "default"
	}

	nginxPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "nginx-",
			Namespace:    namespace,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
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

		_, cond := GetPodCondition(&nginxPod.Status, v1.PodReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
	}).
		WithTimeout(time.Minute).
		WithPolling(time.Second).
		Should(Succeed())

	By(fmt.Sprintf("Nginx Pod is running (%s/%s)", nginxPod.Namespace, nginxPod.Name))

	// only check the pod on the host cluster if the mode is shared mode
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

				return pod.Status.Phase == v1.PodRunning && podIP != ""
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
func (c *VirtualCluster) ExecCmd(pod *v1.Pod, command string) (string, string, error) {
	option := &v1.PodExecOptions{
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
	Eventually(func() any {
		serverPods := listServerPods(ctx, virtualCluster)

		Expect(len(serverPods)).To(Equal(1))

		return serverPods[0].DeletionTimestamp
	}).WithTimeout(60 * time.Second).WithPolling(time.Second * 5).Should(BeNil())
}

func listServerPods(ctx context.Context, virtualCluster *VirtualCluster) []v1.Pod {
	labelSelector := "cluster=" + virtualCluster.Cluster.Name + ",role=server"

	serverPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	Expect(err).To(Not(HaveOccurred()))

	return serverPods.Items
}

func listAgentPods(ctx context.Context, virtualCluster *VirtualCluster) []v1.Pod {
	labelSelector := fmt.Sprintf("cluster=%s,type=agent,mode=%s", virtualCluster.Cluster.Name, virtualCluster.Cluster.Spec.Mode)

	agentPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	Expect(err).To(Not(HaveOccurred()))

	return agentPods.Items
}

// getEnv will get an environment variable from a pod it will return empty string if not found
func getEnv(pod *v1.Pod, envName string) (string, bool) {
	container := pod.Spec.Containers[0]
	for _, envVar := range container.Env {
		if envVar.Name == envName {
			return envVar.Value, true
		}
	}

	return "", false
}

// isArgFound will return true if the argument passed to the function is found in container args
func isArgFound(pod *v1.Pod, arg string) bool {
	container := pod.Spec.Containers[0]
	for _, cmd := range container.Command {
		if strings.Contains(cmd, arg) {
			return true
		}
	}

	return false
}

func getServerIP(ctx context.Context, cfg *rest.Config) (string, error) {
	if k3sContainer != nil {
		return k3sContainer.ContainerIP(ctx)
	}

	u, err := url.Parse(cfg.Host)
	if err != nil {
		return "", err
	}
	// If Host includes a port, u.Hostname() extracts just the hostname part
	return u.Hostname(), nil
}

// GetPodCondition extracts the provided condition from the given status and returns that.
// Returns nil and -1 if the condition is not present, and the index of the located condition.
func GetPodCondition(status *v1.PodStatus, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if status == nil || status.Conditions == nil {
		return -1, nil
	}

	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return i, &status.Conditions[i]
		}
	}

	return -1, nil
}
