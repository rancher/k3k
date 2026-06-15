package k3k_test

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("creating a shared mode cluster", Label(e2eTestLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		namespace := fwk3k.CreateNamespace(k8s)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
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

var _ = When("creating an HCP mode cluster", Label(e2eTestLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		namespace := fwk3k.CreateNamespace(k8s)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
		})

		cluster := NewCluster(namespace.Name)
		cluster.Spec.Mode = v1beta1.HCPClusterMode

		CreateCluster(cluster)
		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})

	It("is up and running", func() {
		Eventually(func(g Gomega) {
			ctx := GinkgoT().Context()
			key := client.ObjectKeyFromObject(virtualCluster.Cluster)
			g.Expect(k8sClient.Get(ctx, key, virtualCluster.Cluster)).To(Succeed())
			g.Expect(virtualCluster.Cluster.Status.Phase).To(BeEquivalentTo(v1beta1.ClusterReady))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})

	It("creates a populated token secret", func() {
		ctx := GinkgoT().Context()

		var tokenSecret corev1.Secret
		err := k8sClient.Get(ctx, client.ObjectKey{
			Name:      k3kcluster.TokenSecretName(virtualCluster.Cluster.Name),
			Namespace: virtualCluster.Cluster.Namespace,
		}, &tokenSecret)
		Expect(err).NotTo(HaveOccurred())
		Expect(tokenSecret.Data["token"]).NotTo(BeEmpty())
	})

	It("reconciles default/kubernetes Endpoints and EndpointSlice", func() {
		ctx := GinkgoT().Context()

		Eventually(func(g Gomega) {
			endpoints, err := virtualCluster.Client.CoreV1().Endpoints("default").Get(ctx, "kubernetes", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(endpoints.Subsets).To(HaveLen(1))
			g.Expect(endpoints.Subsets[0].Addresses).To(HaveLen(1))
			g.Expect(endpoints.Subsets[0].Addresses[0].IP).To(Equal(hostIP))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())

		Eventually(func(g Gomega) {
			slices, err := virtualCluster.Client.DiscoveryV1().EndpointSlices("default").List(ctx, metav1.ListOptions{
				LabelSelector: "kubernetes.io/service-name=kubernetes",
			})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(slices.Items).To(HaveLen(1))
			g.Expect(slices.Items[0].Endpoints).To(HaveLen(1))
			g.Expect(slices.Items[0].Endpoints[0].Addresses).To(ContainElement(hostIP))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})
})
