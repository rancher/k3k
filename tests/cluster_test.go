package k3k_test

import (
	"context"
	"crypto/x509"
	"errors"
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
		ctx := context.Background()
		containerIP, err := k3sContainer.ContainerIP(ctx)
		Expect(err).To(Not(HaveOccurred()))

		fmt.Fprintln(GinkgoWriter, "K3s containerIP: "+containerIP)

		cluster := v1alpha1.Cluster{
			ObjectMeta: v1.ObjectMeta{
				Name:      "mycluster",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				TLSSANs: []string{containerIP},
				Expose: &v1alpha1.ExposeConfig{
					NodePort: &v1alpha1.NodePortConfig{
						Enabled: true,
					},
				},
			},
		}
		virtualK8sClient := CreateCluster(containerIP, cluster)

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
		nginxPod, err = virtualK8sClient.CoreV1().Pods(nginxPod.Namespace).Create(ctx, nginxPod, v1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// check that the nginx Pod is up and running in the host cluster
		Eventually(func() bool {
			//labelSelector := fmt.Sprintf("%s=%s", translate.ClusterNameLabel, cluster.Namespace)
			podList, err := k8s.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{})
			Expect(err).To(Not(HaveOccurred()))

			for _, pod := range podList.Items {
				resourceName := pod.Annotations[translate.ResourceNameAnnotation]
				resourceNamespace := pod.Annotations[translate.ResourceNamespaceAnnotation]

				fmt.Fprintf(GinkgoWriter,
					"pod=%s resource=%s/%s status=%s\n",
					pod.Name, resourceNamespace, resourceName, pod.Status.Phase,
				)

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

	It("will regenerate the bootstrap secret after a restart", func() {
		ctx := context.Background()
		containerIP, err := k3sContainer.ContainerIP(ctx)
		Expect(err).To(Not(HaveOccurred()))

		fmt.Fprintln(GinkgoWriter, "K3s containerIP: "+containerIP)

		cluster := v1alpha1.Cluster{
			ObjectMeta: v1.ObjectMeta{
				Name:      "mycluster",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				TLSSANs: []string{containerIP},
				Expose: &v1alpha1.ExposeConfig{
					NodePort: &v1alpha1.NodePortConfig{
						Enabled: true,
					},
				},
			},
		}
		virtualK8sClient := CreateCluster(containerIP, cluster)

		_, err = virtualK8sClient.DiscoveryClient.ServerVersion()
		Expect(err).To(Not(HaveOccurred()))

		labelSelector := "cluster=" + cluster.Name + ",role=server"
		serverPods, err := k8s.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))

		Expect(len(serverPods.Items)).To(Equal(1))
		serverPod := serverPods.Items[0]

		fmt.Fprintf(GinkgoWriter, "deleting pod %s/%s\n", serverPod.Namespace, serverPod.Name)
		// GracePeriodSeconds: ptr.To[int64](0)
		err = k8s.CoreV1().Pods(namespace).Delete(ctx, serverPod.Name, v1.DeleteOptions{})
		Expect(err).To(Not(HaveOccurred()))

		By("Deleting server pod")

		// check that the server pods restarted
		Eventually(func() any {
			serverPods, err = k8s.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
			Expect(err).To(Not(HaveOccurred()))
			Expect(len(serverPods.Items)).To(Equal(1))
			return serverPods.Items[0].DeletionTimestamp
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeNil())

		By("Server pod up and running again")

		By("Using old k8s client configuration should fail")

		Eventually(func() bool {
			_, err = virtualK8sClient.DiscoveryClient.ServerVersion()
			var unknownAuthorityErr x509.UnknownAuthorityError
			return errors.As(err, &unknownAuthorityErr)
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeTrue())

		By("Recover new config should succeed")

		Eventually(func() error {
			virtualK8sClient = CreateK8sClient(containerIP, cluster)
			_, err = virtualK8sClient.DiscoveryClient.ServerVersion()
			return err
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeNil())
	})
})

func CreateCluster(hostIP string, cluster v1alpha1.Cluster) *kubernetes.Clientset {
	GinkgoHelper()

	By(fmt.Sprintf("Creating virtual cluster %s/%s", cluster.Namespace, cluster.Name))

	ctx := context.Background()
	err := k8sClient.Create(ctx, &cluster)
	Expect(err).To(Not(HaveOccurred()))

	By("Waiting for server and kubelet to be ready")

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

	return CreateK8sClient(hostIP, cluster)
}

func CreateK8sClient(hostIP string, cluster v1alpha1.Cluster) *kubernetes.Clientset {
	GinkgoHelper()

	var err error
	ctx := context.Background()

	By("Waiting for server to be up and running")

	var config *clientcmdapi.Config
	Eventually(func() error {
		vKubeconfig := kubeconfig.New()
		vKubeconfig.AltNames = certs.AddSANs([]string{hostIP, "k3k-mycluster-kubelet"})
		config, err = vKubeconfig.Extract(ctx, k8sClient, &cluster, hostIP)
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

	// serverVersion, err := virtualK8sClient.DiscoveryClient.ServerVersion()
	// Expect(err).To(Not(HaveOccurred()))
	// fmt.Fprintf(GinkgoWriter, "serverVersion: %+v\n", serverVersion)

	return virtualK8sClient
}
