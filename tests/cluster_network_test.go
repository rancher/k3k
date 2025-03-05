package k3k_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
)

var _ = When("two virtual clusters are installed", Label("e2e"), func() {

	var (
		cluster1 *v1alpha1.Cluster
		cluster2 *v1alpha1.Cluster
	)

	BeforeEach(func() {
		createVirtualCluster := func() *v1alpha1.Cluster {
			namespace := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{GenerateName: "ns-"}}

			namespace, err := k8s.CoreV1().Namespaces().Create(context.Background(), namespace, v1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			cluster := &v1alpha1.Cluster{
				ObjectMeta: v1.ObjectMeta{
					GenerateName: "cluster-",
					Namespace:    namespace.Name,
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

			NewVirtualCluster(cluster)

			return cluster
		}

		cluster1 = createVirtualCluster()
		cluster2 = createVirtualCluster()
	})

	It("can create two nginx pods in each of them", func() {

		By("Waiting to get a kubernetes client for the virtual cluster1")
		virtualK8sClient1, restConfig1 := NewVirtualK8sClientAndConfig(cluster1)

		By("Waiting to get a kubernetes client for the virtual cluster2")
		virtualK8sClient2, restConfig2 := NewVirtualK8sClientAndConfig(cluster2)

		createNginxPod := func(kubeclient *kubernetes.Clientset, clusterNamespace, podName string) (*corev1.Pod, string) {
			ctx := context.Background()

			nginxPod := &corev1.Pod{
				ObjectMeta: v1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx",
					}},
				},
			}

			nginxPod, err := kubeclient.CoreV1().Pods(nginxPod.Namespace).Create(ctx, nginxPod, v1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			var podIP string

			// check that the nginx Pod is up and running in the host cluster
			Eventually(func() bool {
				//labelSelector := fmt.Sprintf("%s=%s", translate.ClusterNameLabel, cluster.Namespace)
				podList, err := k8s.CoreV1().Pods(clusterNamespace).List(ctx, v1.ListOptions{})
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
			nginxPod, err = kubeclient.CoreV1().Pods(nginxPod.Namespace).Get(ctx, nginxPod.Name, v1.GetOptions{})
			Expect(err).To(Not(HaveOccurred()))

			return nginxPod, podIP
		}

		pod1Cluster1, pod1Cluster1IP := createNginxPod(virtualK8sClient1, cluster1.Namespace, "nginx-1")
		pod2Cluster1, pod2Cluster1IP := createNginxPod(virtualK8sClient1, cluster1.Namespace, "nginx-2")
		pod1Cluster2, pod1Cluster2IP := createNginxPod(virtualK8sClient2, cluster2.Namespace, "nginx")

		// fmt.Fprintf(GinkgoWriter, "### EXEC1\n%s\n", output)

		var (
			buf     *bytes.Buffer
			curlCmd string
			err     error
		)

		By("Checking that Pods can reach themselves")

		buf = &bytes.Buffer{}
		curlCmd = "curl --no-progress-meter " + pod1Cluster1IP
		err = ExecCmdExample(virtualK8sClient1, restConfig1, pod1Cluster1, curlCmd, buf)
		Expect(err).To(Not(HaveOccurred()))

		buf = &bytes.Buffer{}
		curlCmd = "curl --no-progress-meter " + pod2Cluster1IP
		err = ExecCmdExample(virtualK8sClient1, restConfig1, pod2Cluster1, curlCmd, buf)
		Expect(err).To(Not(HaveOccurred()))

		buf = &bytes.Buffer{}
		curlCmd = "curl --no-progress-meter " + pod1Cluster2IP
		err = ExecCmdExample(virtualK8sClient2, restConfig2, pod1Cluster2, curlCmd, buf)
		Expect(err).To(Not(HaveOccurred()))

		// pods in the same Virtual Cluster should be able to reach each other
		// Pod1 should be able to call Pod2, and viceversa

		By("Checking that Pods in the same virtual clusters can reach each other")

		buf = &bytes.Buffer{}
		curlCmd = "curl --no-progress-meter " + pod2Cluster1IP
		err = ExecCmdExample(virtualK8sClient1, restConfig1, pod1Cluster1, curlCmd, buf)
		Expect(err).To(Not(HaveOccurred()))

		buf = &bytes.Buffer{}
		curlCmd = "curl --no-progress-meter " + pod1Cluster1IP
		err = ExecCmdExample(virtualK8sClient1, restConfig1, pod2Cluster1, curlCmd, buf)
		Expect(err).To(Not(HaveOccurred()))

		By("Checking that Pods in the different virtual clusters cannot reach each other")

		// Pods in Cluster 1 should not be able to reach the Pod in Cluster 2

		buf = &bytes.Buffer{}
		curlCmd = "curl --no-progress-meter " + pod1Cluster2IP
		err = ExecCmdExample(virtualK8sClient1, restConfig1, pod1Cluster1, curlCmd, buf)
		Expect(err).To(HaveOccurred())
		Expect(buf.String()).To(ContainSubstring("Failed to connect"))

		buf = &bytes.Buffer{}
		curlCmd = "curl --no-progress-meter " + pod1Cluster2IP
		err = ExecCmdExample(virtualK8sClient1, restConfig1, pod2Cluster1, curlCmd, buf)
		Expect(err).To(HaveOccurred())
		Expect(buf.String()).To(ContainSubstring("Failed to connect"))

		// Pod in Cluster 2 should not be able to reach Pods in Cluster 1

		buf = &bytes.Buffer{}
		curlCmd = "curl --no-progress-meter " + pod1Cluster1IP
		err = ExecCmdExample(virtualK8sClient2, restConfig2, pod1Cluster2, curlCmd, buf)
		Expect(err).To(HaveOccurred())
		Expect(buf.String()).To(ContainSubstring("Failed to connect"))

		buf = &bytes.Buffer{}
		curlCmd = "curl --no-progress-meter " + pod2Cluster1IP
		err = ExecCmdExample(virtualK8sClient2, restConfig2, pod1Cluster2, curlCmd, buf)
		Expect(err).To(HaveOccurred())
		Expect(buf.String()).To(ContainSubstring("Failed to connect"))
	})
})

// ExecCmd exec command on specific pod and wait the command's output.
func ExecCmdExample(
	client kubernetes.Interface,
	config *rest.Config,
	pod *corev1.Pod,
	command string,
	out io.Writer,
	// stdin io.Reader,
	// stdout io.Writer,
	// stderr io.Writer,
) error {
	option := &corev1.PodExecOptions{
		Command: []string{"sh", "-c", command},
		// Stdin:   true,
		Stdout: true,
		Stderr: true,
		// TTY:     false,
	}

	// if stdin == nil {
	// 	option.Stdin = false
	// }

	req := client.CoreV1().RESTClient().Post().Resource("pods").Name(pod.Name).Namespace(pod.Namespace).SubResource("exec")
	req.VersionedParams(option, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		// Stdin:  stdin,
		Stdout: out,
		Stderr: out,
	})
}
