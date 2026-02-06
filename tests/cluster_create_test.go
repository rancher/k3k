package k3k_test

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("creating a shared mode cluster", Label(e2eTestLabel), Label(slowTestsLabel), func() {
	ctx := context.Background()
	var cluster *v1beta1.Cluster

	BeforeEach(func() {
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster = NewCluster(namespace.Name)
		CreateCluster(cluster)
	})

	It("creates nodes with the worker role", func() {
		client, _ := NewVirtualK8sClientAndConfig(cluster)

		nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))

		node := nodes.Items[0]
		Expect(node.Labels).To(HaveKeyWithValue("node-role.kubernetes.io/worker", "true"))
	})
})
