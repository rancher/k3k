package k3k_test

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

var _ = When("creating a shared mode cluster", Label(lifecycleTestsLabel), Label(slowTestsLabel), func() {
	var (
		virtualCluster *VirtualCluster
		namespace      *corev1.Namespace
	)

	BeforeEach(func() {
		namespace = fwk3k.CreateNamespace(k8s)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
		})

		cluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
			c.Spec.Expose.Annotations = map[string]string{
				"example.com/test": "testing",
			}
			c.Spec.CustomDNS = &v1beta1.CustomDNS{
				Forwarders: []v1beta1.CustomForwarder{{IPs: []string{"8.8.8.8"}}},
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

	It("creates a coredns-custom configmap", func(ctx context.Context) {
		Eventually(func(g Gomega) {
			configMap, err := virtualCluster.Client.CoreV1().ConfigMaps("kube-system").Get(
				ctx, "coredns-custom", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(configMap.Data).To(Equal(map[string]string{
				"custom.override": "    forward . 8.8.8.8\n",
			}))
		}).
			WithTimeout(time.Minute * 1).
			WithPolling(time.Second).
			Should(Succeed())
	})

	It("updates the coredns-custom configmap when the cluster is updated", func(ctx context.Context) {
		Eventually(func(g Gomega) {
			configMap, err := virtualCluster.Client.CoreV1().ConfigMaps("kube-system").Get(
				ctx, "coredns-custom", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(configMap.Data).To(Equal(map[string]string{
				"custom.override": "    forward . 8.8.8.8\n",
			}))
		}).
			WithTimeout(time.Minute * 1).
			WithPolling(time.Second).
			Should(Succeed())

		cluster := virtualCluster.Cluster
		key := client.ObjectKeyFromObject(cluster)
		Expect(k8sClient.Get(ctx, key, cluster)).To(Succeed())

		cluster.Spec.CustomDNS = &v1beta1.CustomDNS{
			Forwarders: []v1beta1.CustomForwarder{{IPs: []string{"8.8.8.8", "1.1.1.1"}}},
		}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		Eventually(func(g Gomega) {
			ctx := GinkgoT().Context()

			configMap, err := virtualCluster.Client.CoreV1().ConfigMaps("kube-system").Get(
				ctx, "coredns-custom", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(configMap.Data).To(Equal(map[string]string{
				"custom.override": "    forward . 8.8.8.8 1.1.1.1\n",
			}))
		}).
			WithTimeout(time.Minute * 1).
			WithPolling(time.Second).
			Should(Succeed())
	})

	It("rejects forwarding with no addresses", func(ctx context.Context) {
		invalidCluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
			c.Spec.CustomDNS = &v1beta1.CustomDNS{
				Forwarders: []v1beta1.CustomForwarder{{IPs: []string{}}},
			}
		})
		err := k8sClient.Create(ctx, invalidCluster)
		Expect(err).To(MatchError(ContainSubstring("ips in body should have at least 1 items")))
	})

	It("rejects invalid IPv4 addresses", func(ctx context.Context) {
		invalidCluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
			c.Spec.CustomDNS = &v1beta1.CustomDNS{
				Forwarders: []v1beta1.CustomForwarder{{IPs: []string{"192.168.1"}}},
			}
		})
		err := k8sClient.Create(ctx, invalidCluster)
		Expect(err).To(MatchError(ContainSubstring("ips[0] in body should match")))
	})
})

var _ = When("creating an HCP mode cluster", Label(lifecycleTestsLabel), Label(slowTestsLabel), func() {
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

		Eventually(func(g Gomega) {
			var tokenSecret corev1.Secret

			key := client.ObjectKey{
				Name:      k3kcluster.TokenSecretName(virtualCluster.Cluster.Name),
				Namespace: virtualCluster.Cluster.Namespace,
			}

			g.Expect(k8sClient.Get(ctx, key, &tokenSecret)).To(Succeed())
			g.Expect(tokenSecret.Data["token"]).NotTo(BeEmpty())
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})

	It("reconciles default/kubernetes Endpoints and EndpointSlice", func() {
		ctx := GinkgoT().Context()

		Eventually(func(g Gomega) {
			key := client.ObjectKeyFromObject(virtualCluster.Cluster)
			g.Expect(k8sClient.Get(ctx, key, virtualCluster.Cluster)).To(Succeed())

			endpoints, err := virtualCluster.Client.CoreV1().Endpoints("default").Get(ctx, "kubernetes", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(endpoints.Subsets).To(HaveLen(1))
			g.Expect(endpoints.Subsets[0].Addresses).To(HaveLen(1))
			g.Expect(virtualCluster.Cluster.Status.TLSSANs).To(ContainElement(endpoints.Subsets[0].Addresses[0].IP))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())

		Eventually(func(g Gomega) {
			key := client.ObjectKeyFromObject(virtualCluster.Cluster)
			g.Expect(k8sClient.Get(ctx, key, virtualCluster.Cluster)).To(Succeed())

			slices, err := virtualCluster.Client.DiscoveryV1().EndpointSlices("default").List(ctx, metav1.ListOptions{
				LabelSelector: "kubernetes.io/service-name=kubernetes",
			})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(slices.Items).To(HaveLen(1))
			g.Expect(slices.Items[0].Endpoints).To(HaveLen(1))
			g.Expect(slices.Items[0].Endpoints[0].Addresses).To(HaveLen(1))
			g.Expect(virtualCluster.Cluster.Status.TLSSANs).To(ContainElement(slices.Items[0].Endpoints[0].Addresses[0]))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})
})
