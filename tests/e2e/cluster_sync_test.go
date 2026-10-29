package e2e_test

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a shared mode cluster is created", Ordered, Label(syncTestsLabel), func() {
	var (
		virtualCluster   *VirtualCluster
		virtualConfigMap *corev1.ConfigMap
		virtualService   *corev1.Service
	)

	BeforeAll(func() {
		virtualCluster = NewVirtualCluster()

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})
	})

	When("a ConfigMap is created in the virtual cluster", func() {
		BeforeAll(func() {
			ctx := GinkgoT().Context()

			virtualConfigMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cm",
					Namespace: "default",
				},
			}

			var err error

			virtualConfigMap, err = virtualCluster.Client.CoreV1().ConfigMaps("default").Create(ctx, virtualConfigMap, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("is replicated in the host cluster", func() {
			ctx := GinkgoT().Context()

			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualConfigMap)

			// check that the ConfigMap is synced in the host cluster
			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().ConfigMaps(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(err).To(Not(HaveOccurred()))
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})

	When("a Service is created in the virtual cluster", func() {
		BeforeAll(func() {
			ctx := GinkgoT().Context()

			virtualService = &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-svc",
					Namespace: "default",
				},
				Spec: corev1.ServiceSpec{
					Type:  corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{{Port: 8888}},
				},
			}

			var err error

			virtualService, err = virtualCluster.Client.CoreV1().Services("default").Create(ctx, virtualService, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("is replicated in the host cluster", func() {
			ctx := GinkgoT().Context()

			// the synced Service keeps its virtual ClusterIP, so it must be in the host Service CIDR
			var hostServiceCIDR networkingv1.ServiceCIDR
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "kubernetes"}, &hostServiceCIDR)).To(Succeed())
			Expect(hostServiceCIDR.Spec.CIDRs).NotTo(BeEmpty())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(virtualCluster.Cluster), virtualCluster.Cluster)).To(Succeed())
			Expect(virtualCluster.Cluster.Status.ServiceCIDR).To(Equal(hostServiceCIDR.Spec.CIDRs[0]))

			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualService)

			// check that the Service is synced in the host cluster
			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().Services(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(err).To(Not(HaveOccurred()))
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})
	When("the secrets sync is updated with a set based label selector", func() {
		BeforeAll(func() {
			ctx := GinkgoT().Context()

			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(virtualCluster.Cluster), virtualCluster.Cluster)
			Expect(err).To(Not(HaveOccurred()))

			virtualCluster.Cluster.Spec.Sync.Secrets = v1beta1.SecretSyncConfig{
				Enabled: true,
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "tier",
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{"frontend", "backend"},
						},
					},
				},
			}
			err = k8sClient.Update(ctx, virtualCluster.Cluster)
			Expect(err).To(Not(HaveOccurred()))
		})
		It("sync secrets with labels in set", func() {
			ctx := GinkgoT().Context()
			virtualSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret-in-set",
					Namespace: "default",
					Labels: map[string]string{
						"tier": "frontend",
					},
				},
			}

			virtualSecret, err := virtualCluster.Client.CoreV1().Secrets("default").Create(ctx, virtualSecret, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualSecret)

			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().Secrets(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(err).To(Not(HaveOccurred()))
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})

		It("will not sync secrets with labels not in set", func() {
			ctx := GinkgoT().Context()
			virtualSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret-not-in-set",
					Namespace: "default",
					Labels: map[string]string{
						"tier": "cache",
					},
				},
			}

			virtualSecret, err := virtualCluster.Client.CoreV1().Secrets("default").Create(ctx, virtualSecret, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualSecret)

			Consistently(func(g Gomega) {
				_, err := k8s.CoreV1().Secrets(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).
				WithTimeout(time.Second * 15).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})

	When("the cluster is deleted", func() {
		BeforeAll(func() {
			ctx := context.Background()

			By("Deleting cluster")

			err := k8sClient.Delete(ctx, virtualCluster.Cluster)
			Expect(err).To(Not(HaveOccurred()))
		})

		It("will delete the ConfigMap from the host cluster", func() {
			ctx := context.Background()

			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualConfigMap)

			// check that the ConfigMap is deleted from the host cluster
			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().ConfigMaps(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})

		It("will delete the Service from the host cluster", func() {
			ctx := context.Background()

			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualService)

			// check that the Service is deleted from the host cluster
			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().Services(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})
})
