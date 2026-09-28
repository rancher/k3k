package e2e_test

import (
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("Testing pod lifecycle event mirroring in shared mode cluster", Label(lifecycleTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	// BeforeEach sets up a new shared mode virtual cluster for each test case
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

	It("Verifies a shared mode cluster's pod lifecycle events match the host cluster", func() {
		ctx := GinkgoT().Context()
		virtNs := "virt-test-namespace"
		podName := "sync-test-pod"

		By(fmt.Sprintf("Creating namespace %s in virtual cluster", virtNs))
		_, err := virtualCluster.Client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: virtNs},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Creating pod %s in virtual cluster namespace %s", podName, virtNs))
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: podName,
				Labels: map[string]string{
					"app": "event-sync-test",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "nginx",
						Image: "nginx:alpine",
					},
				},
			},
		}
		_, err = virtualCluster.Client.CoreV1().Pods(virtNs).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for pod to run and fetching events from both clusters to verify sync")

		expectedReasons := []string{"Scheduled", "Pulling", "Pulled", "Created", "Started"}

		// Wait for the pod to be running and verify that lifecycle events are mirrored between host cluster and virtual shared mode cluster correctly
		Eventually(func(g Gomega) {
			p, err := virtualCluster.Client.CoreV1().Pods(virtNs).Get(ctx, podName, metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(p.Status.Phase).To(Equal(corev1.PodRunning))

			hostPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=event-sync-test",
			})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(hostPods.Items).NotTo(BeEmpty(), "corresponding pod not yet found in host cluster")
			hostPodName := hostPods.Items[0].Name

			hostEvents, err := k8s.CoreV1().Events(virtualCluster.Cluster.Namespace).List(ctx, metav1.ListOptions{
				FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Pod", hostPodName),
			})
			g.Expect(err).To(Not(HaveOccurred()))

			virtEvents, err := virtualCluster.Client.CoreV1().Events(virtNs).List(ctx, metav1.ListOptions{
				FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Pod", podName),
			})
			g.Expect(err).To(Not(HaveOccurred()))

			virtEventMap := make(map[string]bool)
			for _, ve := range virtEvents.Items {
				virtEventMap[ve.Reason] = true
			}

			g.Expect(virtEventMap).NotTo(BeEmpty(), "no events have synced to the virtual cluster yet")

			for _, he := range hostEvents.Items {
				if slices.Contains(expectedReasons, he.Reason) {
					g.Expect(virtEventMap).To(HaveKey(he.Reason), fmt.Sprintf("Host event '%s' did not sync to virtual cluster", he.Reason))
				}
			}
		}).
			WithTimeout(2 * time.Minute).
			WithPolling(2 * time.Second).
			Should(Succeed())
	})
})
