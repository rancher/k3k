package k3k_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func NewVirtualCluster(cluster v1alpha1.Cluster) {
	GinkgoHelper()

	ctx := context.Background()
	err := k8sClient.Create(ctx, &cluster)
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
func NewVirtualK8sClient(cluster v1alpha1.Cluster) *kubernetes.Clientset {
	GinkgoHelper()

	var err error
	ctx := context.Background()

	var config *clientcmdapi.Config
	Eventually(func() error {
		vKubeconfig := kubeconfig.New()
		kubeletAltName := fmt.Sprintf("k3k-%s-kubelet", cluster.Name)
		vKubeconfig.AltNames = certs.AddSANs([]string{hostIP, kubeletAltName})
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

	return virtualK8sClient
}
