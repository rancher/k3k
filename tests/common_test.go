package k3k_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type VirtualCluster struct {
	Cluster    *v1alpha1.Cluster
	RestConfig *rest.Config
	Client     *kubernetes.Clientset
}

func NewVirtualCluster() *VirtualCluster {
	GinkgoHelper()

	namespace := NewNamespace()

	By(fmt.Sprintf("Creating new virtual cluster in namespace %s", namespace.Name))
	cluster := NewCluster(namespace.Name)
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

	// check that the server Pod and the Kubelet are in Ready state
	Eventually(func() bool {
		podList, err := k8s.CoreV1().Pods(cluster.Namespace).List(ctx, v1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))

		serverRunning := false
		kubeletRunning := false

		for _, pod := range podList.Items {
			imageName := pod.Spec.Containers[0].Image
			imageName = strings.Split(imageName, ":")[0] // remove tag

			switch imageName {
			case "rancher/k3s":
				serverRunning = pod.Status.Phase == corev1.PodRunning
			case "rancher/k3k-kubelet":
				kubeletRunning = pod.Status.Phase == corev1.PodRunning
			}

			if serverRunning && kubeletRunning {
				return true
			}
		}

		return false
	}).
		WithTimeout(time.Minute * 2).
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
		config, err = vKubeconfig.Extract(ctx, k8sClient, cluster, hostIP)
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

				fmt.Fprintf(GinkgoWriter,
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
