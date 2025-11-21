package k3k_test

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a shared mode cluster is created", Ordered, Label(e2eTestLabel), func() {
	var (
		virtualCluster   *VirtualCluster
		virtualConfigMap *corev1.ConfigMap
		virtualService   *corev1.Service
	)

	BeforeAll(func() {
		virtualCluster = NewVirtualCluster()

		DeferCleanup(func() {
			DeleteNamespaces(virtualCluster.Cluster.Namespace)
		})
	})

	When("a ConfigMap is created in the virtual cluster", func() {
		BeforeAll(func() {
			ctx := context.Background()

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
			ctx := context.Background()

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
			ctx := context.Background()

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
			ctx := context.Background()

			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualService)

			// check that the ConfigMap is synced in the host cluster
			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().Services(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(err).To(Not(HaveOccurred()))
			}).
				WithTimeout(time.Minute).
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
