package k3k_test

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

var _ = When("creating a shared mode cluster", Label(e2eTestLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		namespace := fwk3k.CreateNamespace(k8s)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
		})

		cluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
			c.Spec.Expose.Annotations = map[string]string{
				"example.com/test": "testing",
			}
		})
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
			ctx := GinkgoT().Context()

			nodes, err := virtualCluster.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(nodes.Items).To(HaveLen(1))
			g.Expect(nodes.Items[0].Labels).To(HaveKeyWithValue("node-role.kubernetes.io/worker", "true"))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})

	It("creates services with annotations", func() {
		Eventually(func(g Gomega) {
			ctx := GinkgoT().Context()

			cluster := virtualCluster.Cluster
			service, err := k8s.CoreV1().Services(cluster.Namespace).Get(
				ctx, "k3k-"+cluster.GetName()+"-service", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))

			g.Expect(service.GetAnnotations()).To(MatchAllKeys(Keys{
				"example.com/test": Equal("testing"),
			}))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})

	It("updates the annotations when the cluster is updated", func() {
		// Wait for Service to be created.
		ctx := GinkgoT().Context()
		cluster := virtualCluster.Cluster

		Eventually(func(g Gomega) {
			service, err := k8s.CoreV1().Services(cluster.Namespace).Get(
				ctx, "k3k-"+cluster.GetName()+"-service", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))

			g.Expect(service.GetAnnotations()).To(MatchAllKeys(Keys{
				"example.com/test": Equal("testing"),
			}))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())

		service, err := k8s.CoreV1().Services(cluster.Namespace).Get(
			ctx, "k3k-"+cluster.GetName()+"-service", metav1.GetOptions{})
		Expect(err).To(Not(HaveOccurred()))

		service.Annotations["example.com/other-annotation"] = "retain-this"
		_, err = k8s.CoreV1().Services(cluster.Namespace).Update(ctx, service, metav1.UpdateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// Reload cluster
		key := client.ObjectKeyFromObject(cluster)
		Expect(k8sClient.Get(ctx, key, cluster)).To(Succeed())

		// Update annotations
		cluster.Spec.Expose.Annotations = map[string]string{
			"example.com/test": "updated",
		}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		Eventually(func(g Gomega) {
			service, err := k8s.CoreV1().Services(cluster.Namespace).Get(
				ctx, "k3k-"+cluster.GetName()+"-service", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))

			g.Expect(service.GetAnnotations()).To(MatchAllKeys(Keys{
				"example.com/test":             Equal("updated"),
				"example.com/other-annotation": Equal("retain-this"),
			}))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})

	It("has the provider.cattle.io label set to k3k", func() {
		Eventually(func(g Gomega) {
			ctx := GinkgoT().Context()

			key := client.ObjectKeyFromObject(virtualCluster.Cluster)
			g.Expect(k8sClient.Get(ctx, key, virtualCluster.Cluster)).To(Succeed())
			g.Expect(virtualCluster.Cluster.Labels).To(HaveKeyWithValue("provider.cattle.io", "k3k"))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})
})
