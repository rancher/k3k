package k3k_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/utils/ptr"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = FWhen("a server pod is restarted", Label("e2e"), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		GinkgoHelper()

		namespace := NewNamespace()

		By(fmt.Sprintf("Creating new virtual cluster in namespace %s", namespace.Name))

		cluster := NewCluster(namespace.Name)
		cluster.Spec.Servers = ptr.To[int32](3)

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		By(fmt.Sprintf("Created virtual cluster %s/%s", cluster.Namespace, cluster.Name))

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})

	AfterEach(func() {
		DeleteNamespaces(virtualCluster.Cluster.Namespace)
	})

	FIt("still works", func() {
		ctx := context.Background()

		_, err := virtualCluster.Client.ServerVersion()
		Expect(err).To(Not(HaveOccurred()))

		labelSelector := "cluster=" + virtualCluster.Cluster.Name + ",role=server"
		serverPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))

		Expect(len(serverPods.Items)).To(Equal(3))
		serverPod := serverPods.Items[1]

		GinkgoWriter.Printf("deleting pod %s/%s\n", serverPod.Namespace, serverPod.Name)

		err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).Delete(ctx, serverPod.Name, v1.DeleteOptions{})
		Expect(err).To(Not(HaveOccurred()))

		By("Deleting server pod")

		// check that the server pods restarted
		Eventually(func() any {
			serverPods, err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
			Expect(err).To(Not(HaveOccurred()))
			Expect(len(serverPods.Items)).To(Equal(3))

			for _, pod := range serverPods.Items {
				if pod.DeletionTimestamp != nil {
					return pod.DeletionTimestamp
				}
			}

			return nil
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeNil())

		By("Server pod up and running again")

		By("Using old k8s client configuration should succeed")

		Eventually(func() error {
			info, err := virtualCluster.Client.DiscoveryClient.ServerVersion()
			By(fmt.Sprintf("info=%+v", info))

			return err
		}).
			MustPassRepeatedly(10).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeNil())
	})
})
