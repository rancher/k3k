package k3k_test

import (
	"context"
	"strings"
	"time"

	"k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression test for the mode-flip bug: scaling Servers across the 1 boundary
// used to flip the startup command (single<->ha), which rolled every server pod
// (including server-0) and left etcd membership pointing at stale pod IPs, so the
// cluster could not reform. The fix makes the rendered command independent of the
// server count, so scaling only adds/removes the top ordinals and never rolls
// server-0.
var _ = When("a persistent HA cluster is scaled across the single-server boundary",
	Label(e2eTestLabel), Label(updateTestsLabel), Label(slowTestsLabel), func() {
		var virtualCluster *VirtualCluster

		ctx := context.Background()

		BeforeEach(func() {
			namespace := fwk3k.CreateNamespace(k8s)

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, namespace.Name)
			})

			cluster := NewCluster(namespace.Name)
			cluster.Spec.Servers = ptr.To[int32](3)
			cluster.Spec.Persistence = v1beta1.PersistenceConfig{
				Type: v1beta1.DynamicPersistenceMode,
			}

			CreateCluster(cluster) // waits until all 3 servers are Ready

			client, restConfig := NewVirtualK8sClientAndConfig(cluster)
			virtualCluster = &VirtualCluster{
				Cluster:    cluster,
				RestConfig: restConfig,
				Client:     client,
			}

			Expect(listServerPods(ctx, virtualCluster)).To(HaveLen(3))
		})

		It("scales 3->1->3 without rolling server-0 and stays healthy", func() {
			scaleServers := func(n int32) {
				var c v1beta1.Cluster
				Expect(k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &c)).To(Succeed())
				c.Spec.Servers = ptr.To(n)
				Expect(k8sClient.Update(ctx, &c)).To(Succeed())
			}

			// UID of the server-0 pod; if it changes, the pod was recreated.
			serverZeroUID := func() apitypes.UID {
				for _, serverPod := range listServerPods(ctx, virtualCluster) {
					if strings.HasSuffix(serverPod.Name, "-server-0") {
						return serverPod.UID
					}
				}

				return ""
			}

			originalUID := serverZeroUID()
			Expect(originalUID).NotTo(BeEmpty())

			By("Scaling down the servers from 3 to 1")
			scaleServers(1)

			Eventually(func(g Gomega) {
				g.Expect(listServerPods(ctx, virtualCluster)).To(HaveLen(1))
			}).
				WithTimeout(time.Minute * 3).
				WithPolling(time.Second * 5).
				Should(Succeed())

			By("server-0 must NOT have been recreated by the scale down")
			Expect(serverZeroUID()).To(Equal(originalUID))

			By("Scaling the servers back up from 1 to 3")
			scaleServers(3)

			By("All servers become Ready again")
			Eventually(func(g Gomega) {
				serverPods := listServerPods(ctx, virtualCluster)
				g.Expect(serverPods).To(HaveLen(3))

				for _, serverPod := range serverPods {
					_, cond := pod.GetPodCondition(&serverPod.Status, corev1.PodReady)
					g.Expect(cond).NotTo(BeNil())
					g.Expect(cond.Status).To(BeEquivalentTo(corev1.ConditionTrue))
				}
			}).
				WithTimeout(time.Minute * 8).
				WithPolling(time.Second * 10).
				Should(Succeed())

			By("server-0 remained the same pod throughout")
			Expect(serverZeroUID()).To(Equal(originalUID))
		})
	})
