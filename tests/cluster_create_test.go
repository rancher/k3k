package k3k_test

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("creating a shared mode cluster", Label(e2eTestLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		namespace := NewNamespace()
		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)
		CreateCluster(cluster)
		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})

	It("creates nodes with the worker role", func() {
		Eventually(func(g Gomega) {
			nodes, err := virtualCluster.Client.CoreV1().Nodes().List(GinkgoT().Context(), metav1.ListOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(nodes.Items).To(HaveLen(1))
			g.Expect(nodes.Items[0].Labels).To(HaveKeyWithValue("node-role.kubernetes.io/worker", "true"))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})
})
