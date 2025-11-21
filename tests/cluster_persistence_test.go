package k3k_test

import (
	"context"
	"crypto/x509"
	"errors"
	"time"

	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("an ephemeral cluster is installed", Label(e2eTestLabel), Label(persistenceTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		virtualCluster = NewVirtualCluster()

		DeferCleanup(func() {
			DeleteNamespaces(virtualCluster.Cluster.Namespace)
		})
	})

	It("can create a nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})

	It("deletes the pod in the virtual cluster when deleted from the host", func() {
		ctx := context.Background()
		pod, _ := virtualCluster.NewNginxPod("")

		hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
		namespacedName := hostTranslator.NamespacedName(pod)

		err := k8s.CoreV1().Pods(namespacedName.Namespace).Delete(ctx, namespacedName.Name, v1.DeleteOptions{})
		Expect(err).To(Not(HaveOccurred()))

		Eventually(func() bool {
			_, err := virtualCluster.Client.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, v1.GetOptions{})
			return apierrors.IsNotFound(err)
		}).
			WithPolling(time.Second * 5).
			WithTimeout(time.Minute).
			Should(BeTrue())
	})

	It("regenerates the bootstrap secret after a restart", func() {
		ctx := context.Background()

		_, err := virtualCluster.Client.ServerVersion()
		Expect(err).To(Not(HaveOccurred()))

		labelSelector := "cluster=" + virtualCluster.Cluster.Name + ",role=server"
		serverPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))

		Expect(len(serverPods.Items)).To(Equal(1))
		serverPod := serverPods.Items[0]

		GinkgoWriter.Printf("deleting pod %s/%s\n", serverPod.Namespace, serverPod.Name)

		err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).Delete(ctx, serverPod.Name, v1.DeleteOptions{})
		Expect(err).To(Not(HaveOccurred()))

		By("Deleting server pod")

		// check that the server pods restarted
		Eventually(func() any {
			serverPods, err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
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
			_, err = virtualCluster.Client.DiscoveryClient.ServerVersion()
			var unknownAuthorityErr x509.UnknownAuthorityError
			return errors.As(err, &unknownAuthorityErr)
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeTrue())

		By("Recover new config should succeed")

		Eventually(func() error {
			virtualCluster.Client, virtualCluster.RestConfig = NewVirtualK8sClientAndConfig(virtualCluster.Cluster)
			_, err = virtualCluster.Client.DiscoveryClient.ServerVersion()
			return err
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeNil())
	})
})

var _ = When("a dynamic cluster is installed", Label(e2eTestLabel), Label(persistenceTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		virtualCluster = NewVirtualClusterWithType(v1beta1.DynamicPersistenceMode)
	})

	AfterEach(func() {
		DeleteNamespaces(virtualCluster.Cluster.Namespace)
	})

	It("can create a nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})

	It("can delete the cluster", func() {
		ctx := context.Background()

		By("Deleting cluster")

		err := k8sClient.Delete(ctx, virtualCluster.Cluster)
		Expect(err).To(Not(HaveOccurred()))

		Eventually(func() []corev1.Pod {
			By("listing the pods in the namespace")

			podList, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{})
			Expect(err).To(Not(HaveOccurred()))

			GinkgoLogr.Info("podlist", "len", len(podList.Items))

			return podList.Items
		}).
			WithTimeout(2 * time.Minute).
			WithPolling(time.Second).
			Should(BeEmpty())
	})

	It("can delete a HA cluster", func() {
		ctx := context.Background()

		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(virtualCluster.Cluster.Namespace)
		})

		cluster := NewCluster(namespace.Name)
		cluster.Spec.Persistence.Type = v1beta1.DynamicPersistenceMode
		cluster.Spec.Servers = ptr.To[int32](2)

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster := &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}

		By("Deleting cluster")

		err := k8sClient.Delete(ctx, virtualCluster.Cluster)
		Expect(err).To(Not(HaveOccurred()))

		Eventually(func() []corev1.Pod {
			By("listing the pods in the namespace")

			podList, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{})
			Expect(err).To(Not(HaveOccurred()))

			GinkgoLogr.Info("podlist", "len", len(podList.Items))

			return podList.Items
		}).
			WithTimeout(time.Minute * 3).
			WithPolling(time.Second).
			Should(BeEmpty())
	})

	It("uses the same bootstrap secret after a restart", func() {
		ctx := context.Background()

		_, err := virtualCluster.Client.ServerVersion()
		Expect(err).To(Not(HaveOccurred()))

		restartServerPod(ctx, virtualCluster)

		By("Server pod up and running again")

		By("Using old k8s client configuration should succeed")

		Eventually(func() error {
			_, err = virtualCluster.Client.DiscoveryClient.ServerVersion()
			return err
		}).
			WithTimeout(2 * time.Minute).
			WithPolling(time.Second * 5).
			Should(BeNil())
	})
})
