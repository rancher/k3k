package k3k_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
			},
		}

		err := k8sClient.Create(context.Background(), &cluster)
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

		By("checking the existence of the bootstrap secret")
		secretName := fmt.Sprintf("k3k-%s-bootstrap", cluster.Name)

		Eventually(func() error {
			_, err := k8s.CoreV1().Secrets(namespace).Get(context.Background(), secretName, v1.GetOptions{})
			return err
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeNil())
	})
})
