package k3k_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"
)

var _ = When("a cluster is installed", func() {

	var namespace string

	BeforeEach(func() {
		createdNS := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{GenerateName: "ns-"}}
		createdNS, err := k8s.CoreV1().Namespaces().Create(context.Background(), createdNS, v1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))
		namespace = createdNS.Name
	})

	It("will be created in shared mode", func() {
		containerIP, err := k3sContainer.ContainerIP(context.Background())
		Expect(err).To(Not(HaveOccurred()))

		fmt.Fprintln(GinkgoWriter, "ip: "+containerIP)

		cluster := v1alpha1.Cluster{
			ObjectMeta: v1.ObjectMeta{
				Name:      "mycluster",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				Mode:    v1alpha1.SharedClusterMode,
				Servers: ptr.To[int32](1),
				Agents:  ptr.To[int32](0),
				Version: "v1.26.1-k3s1",
				TLSSANs: []string{containerIP},
				Expose: &v1alpha1.ExposeConfig{
					NodePort: &v1alpha1.NodePortConfig{
						Enabled: true,
					},
				},
			},
		}

		err = k8sClient.Create(context.Background(), &cluster)
		Expect(err).To(Not(HaveOccurred()))

		By("checking server and kubelet readiness state")

		// check that the server Pod and the Kubelet are in Ready state
		Eventually(func() bool {
			podList, err := k8s.CoreV1().Pods(namespace).List(context.Background(), v1.ListOptions{})
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
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeTrue())

		By("Waiting for server to be ready")

		var config *clientcmdapi.Config
		Eventually(func() error {
			vKubeconfig := kubeconfig.New()
			vKubeconfig.AltNames = certs.AddSANs([]string{containerIP, "k3k-mycluster-kubelet"})
			config, err = vKubeconfig.Extract(context.Background(), k8sClient, &cluster, containerIP)
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

		serverVersion, err := virtualK8sClient.DiscoveryClient.ServerVersion()
		Expect(err).To(Not(HaveOccurred()))
		fmt.Fprintf(GinkgoWriter, "serverVersion: %+v\n", serverVersion)

		nginxPod := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "nginx",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "nginx",
					Image: "nginx",
				}},
			},
		}
		nginxPod, err = virtualK8sClient.CoreV1().Pods(nginxPod.Namespace).Create(context.Background(), nginxPod, v1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// check that the nginx Pod is up and running in the host cluster
		Eventually(func() bool {
			//labelSelector := fmt.Sprintf("%s=%s", translate.ClusterNameLabel, cluster.Namespace)
			podList, err := k8s.CoreV1().Pods(namespace).List(context.Background(), v1.ListOptions{})
			Expect(err).To(Not(HaveOccurred()))

			for _, pod := range podList.Items {
				resourceName := pod.Annotations[translate.ResourceNameAnnotation]
				resourceNamespace := pod.Annotations[translate.ResourceNamespaceAnnotation]

				fmt.Fprintf(GinkgoWriter, "pod=%s resource=%s/%s\n", pod.Name, resourceNamespace, resourceName)

				if resourceName == nginxPod.Name && resourceNamespace == nginxPod.Namespace {
					return pod.Status.Phase == corev1.PodRunning
				}
			}

			return false
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeTrue())
	})
})
