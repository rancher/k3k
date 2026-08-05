package k3k_test

import (
	"context"
	"io"
	"time"

	"k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Verifies the rejoin liveness probe end-to-end (rancher/k3k#537). Scaling a
// persistent HA cluster down (2->1) removes the etcd member for ordinal 1 while
// its PVC is retained; scaling back up (1->2) recreates the ordinal, which
// re-mounts the old PVC whose etcd data references an already-removed member.
// k3s then logs "...please restart k3s to rejoin the cluster", so run_k3s writes
// the /var/log/k3s-rejoin sentinel and the liveness probe (test -f that sentinel)
// restarts the container. We assert (a) the sentinel is actually created by
// run_k3s, (b) the container is restarted, and (c) the servers recover to Ready.
var _ = FWhen("a persistent HA server rejoins after a scale down and up",
	Label(e2eTestLabel), Label(updateTestsLabel), Label(slowTestsLabel), func() {
		var virtualCluster *VirtualCluster

		ctx := context.Background()

		BeforeEach(func() {
			namespace := fwk3k.CreateNamespace(k8s)

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, namespace.Name)
			})

			cluster := NewCluster(namespace.Name)

			// HA setup with persistent storage: each server ordinal gets its own
			// retained PVC, which is the precondition for the "rejoin" condition.
			cluster.Spec.Servers = ptr.To[int32](2)
			cluster.Spec.Persistence = v1beta1.PersistenceConfig{
				Type: v1beta1.DynamicPersistenceMode,
			}

			CreateCluster(cluster) // waits until both servers are Ready

			client, restConfig := NewVirtualK8sClientAndConfig(cluster)
			virtualCluster = &VirtualCluster{
				Cluster:    cluster,
				RestConfig: restConfig,
				Client:     client,
			}

			Expect(listServerPods(ctx, virtualCluster)).To(HaveLen(2))
		})

		It("has the rejoin sentinel written, is restarted by the liveness probe, and recovers", func() {
			scaleServers := func(n int32) {
				var c v1beta1.Cluster
				Expect(k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &c)).To(Succeed())
				c.Spec.Servers = ptr.To[int32](n)
				Expect(k8sClient.Update(ctx, &c)).To(Succeed())
			}

			By("Scaling down the servers from 2 to 1")
			// Scaling down removes the etcd member for ordinal 1 (via the
			// etcdpod.k3k.io finalizer), while its PVC is retained.
			scaleServers(1)

			Eventually(func(g Gomega) {
				g.Expect(listServerPods(ctx, virtualCluster)).To(HaveLen(1))
			}).
				WithTimeout(time.Minute * 3).
				WithPolling(time.Second * 5).
				Should(Succeed())

			By("Scaling up the servers from 1 back to 2")
			// The recreated ordinal re-mounts its old PVC, whose etcd data
			// references a member that was already removed. k3s then logs
			// "...please restart k3s to rejoin the cluster" and run_k3s writes
			// the sentinel that the liveness probe checks.
			scaleServers(2)

			By("Verifying run_k3s created the rejoin sentinel on a recreated server")
			// The sentinel is short-lived: run_k3s clears it on every restart, so
			// we poll fast to catch it in the window before the liveness probe
			// restarts the container.
			Eventually(func(g Gomega) {
				serverPods := listServerPods(ctx, virtualCluster)

				found := false

				for _, serverPod := range serverPods {
					_, err := podExec(ctx, k8s, restcfg, serverPod.Namespace, serverPod.Name,
						[]string{"sh", "-c", "test -f /var/log/k3s-rejoin"}, nil, io.Discard)
					if err == nil {
						found = true
						break
					}
				}

				g.Expect(found).To(BeTrue())
			}).
				WithTimeout(time.Minute * 3).
				WithPolling(time.Second).
				Should(Succeed())

			By("Verifying the liveness probe restarts a recreated server")
			Eventually(func(g Gomega) {
				serverPods := listServerPods(ctx, virtualCluster)
				g.Expect(serverPods).To(HaveLen(2))

				restarted := 0

				for _, serverPod := range serverPods {
					if len(serverPod.Status.ContainerStatuses) > 0 && serverPod.Status.ContainerStatuses[0].RestartCount > 0 {
						restarted++
					}
				}

				g.Expect(restarted).To(BeNumerically(">=", 1))
			}).
				WithTimeout(time.Minute * 5).
				WithPolling(time.Second * 5).
				Should(Succeed())

			By("Verifying all servers recover to Ready")
			Eventually(func(g Gomega) {
				serverPods := listServerPods(ctx, virtualCluster)
				g.Expect(serverPods).To(HaveLen(2))

				for _, serverPod := range serverPods {
					_, cond := pod.GetPodCondition(&serverPod.Status, corev1.PodReady)
					g.Expect(cond).NotTo(BeNil())
					g.Expect(cond.Status).To(BeEquivalentTo(corev1.ConditionTrue))
				}
			}).
				WithTimeout(time.Minute * 5).
				WithPolling(time.Second * 10).
				Should(Succeed())
		})
	})
