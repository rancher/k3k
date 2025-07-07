package cluster_test

import (
	"context"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/policy"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Cluster Status", func() {

	var (
		namespace string
		vcp       *v1alpha1.VirtualClusterPolicy
	)

	// This BeforeEach/AfterEach will create a new namespace and a default policy for each test.
	BeforeEach(func() {
		createdNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"}}
		err := k8sClient.Create(context.Background(), createdNS)
		Expect(err).To(Not(HaveOccurred()))
		namespace = createdNS.Name

		vcp = &v1alpha1.VirtualClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "policy-",
			},
		}

		err = k8sClient.Create(ctx, vcp)
		Expect(err).To(Not(HaveOccurred()))

		createdNS.Labels = map[string]string{
			policy.PolicyNameLabelKey: vcp.Name,
		}
		Expect(k8sClient.Update(ctx, createdNS)).To(Not(HaveOccurred()))
	})

	AfterEach(func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Delete(ctx, ns)).To(Not(HaveOccurred()))

		Expect(k8sClient.Delete(ctx, vcp)).To(Not(HaveOccurred()))
	})

	Context("when a new cluster is created", func() {

		It("should start with Provisioning status and transition to Ready", func() {
			vCluster := &v1alpha1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "cluster-",
					Namespace:    namespace,
				},
			}

			err := k8sClient.Create(ctx, vCluster)
			Expect(err).To(Not(HaveOccurred()))

			// Check for the initial status to be set
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vCluster), vCluster)
				Expect(err).NotTo(HaveOccurred())

				Expect(vCluster.Status.OverallStatus).To(Equal(v1alpha1.ClusterProvisioning))
				cond := meta.FindStatusCondition(vCluster.Status.Conditions, cluster.ConditionReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(cluster.ReasonProvisioning))
			}).WithTimeout(time.Second * 10).Should(Succeed())

			// Check for the status to be updated to Ready
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vCluster), vCluster)
				Expect(err).NotTo(HaveOccurred())

				Expect(vCluster.Status.OverallStatus).To(Equal(v1alpha1.ClusterReady))
				cond := meta.FindStatusCondition(vCluster.Status.Conditions, cluster.ConditionReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal(cluster.ReasonProvisioned))
			}).WithTimeout(time.Second * 30).WithPolling(time.Second).Should(Succeed())
		})
	})

	Context("when cluster has validation errors", func() {
		It("should be in Pending status with ValidationFailed reason", func() {
			vCluster := &v1alpha1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "cluster-",
					Namespace:    namespace,
				},
				Spec: v1alpha1.ClusterSpec{
					Mode: v1alpha1.VirtualClusterMode,
				},
			}

			err := k8sClient.Create(ctx, vCluster)
			Expect(err).To(Not(HaveOccurred()))

			// Check for the status to be updated
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vCluster), vCluster)
				Expect(err).NotTo(HaveOccurred())

				Expect(vCluster.Status.OverallStatus).To(Equal(v1alpha1.ClusterPending))

				cond := meta.FindStatusCondition(vCluster.Status.Conditions, cluster.ConditionReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(cluster.ReasonValidationFailed))
			}).WithTimeout(time.Second * 10).Should(Succeed())
		})
	})

	Context("when cluster is being deleted", func() {
		It("should transition to Terminating status", func() {
			vCluster := &v1alpha1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "terminating-cluster-",
					Namespace:    namespace,
				},
			}
			Expect(k8sClient.Create(ctx, vCluster)).To(Succeed())

			// Wait for finalizer to be added
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vCluster), vCluster)
				Expect(err).NotTo(HaveOccurred())
				Expect(vCluster.Finalizers).To(ContainElement(cluster.ClusterFinalizerName))
			}).WithTimeout(time.Second * 10).Should(Succeed())

			// Delete the cluster
			Expect(k8sClient.Delete(ctx, vCluster)).To(Succeed())

			// Check for the status to be updated to Terminating
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vCluster), vCluster)
				Expect(err).NotTo(HaveOccurred())

				Expect(vCluster.Status.OverallStatus).To(Equal(v1alpha1.ClusterTerminating))

				cond := meta.FindStatusCondition(vCluster.Status.Conditions, cluster.ConditionReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(cluster.ReasonTerminating))
			}).WithTimeout(time.Second * 10).Should(Succeed())
		})
	})
})
