package k3k_test

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	schedv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/policy"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a cluster's status is tracked", Label(e2eTestLabel), Label(statusTestsLabel), func() {
	var (
		namespace *corev1.Namespace
		vcp       *v1beta1.VirtualClusterPolicy
	)

	// This BeforeEach/AfterEach will create a new namespace and a default policy for each test.
	BeforeEach(func() {
		ctx := context.Background()

		vcp = &v1beta1.VirtualClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "policy-",
			},
		}
		Expect(k8sClient.Create(ctx, vcp)).To(Succeed())

		namespace = NewNamespace()

		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(namespace), namespace)
		Expect(err).To(Not(HaveOccurred()))

		namespace.Labels = map[string]string{
			policy.PolicyNameLabelKey: vcp.Name,
		}
		Expect(k8sClient.Update(ctx, namespace)).To(Succeed())
	})

	AfterEach(func() {
		err := k8sClient.Delete(context.Background(), vcp)
		Expect(err).To(Not(HaveOccurred()))

		DeleteNamespaces(namespace.Name)
	})

	Context("and the cluster is created with a valid configuration", func() {
		It("should start with Provisioning status and transition to Ready", func() {
			ctx := context.Background()

			clusterObj := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "status-cluster-",
					Namespace:    namespace.Name,
				},
			}
			Expect(k8sClient.Create(ctx, clusterObj)).To(Succeed())

			clusterKey := client.ObjectKeyFromObject(clusterObj)

			// Check for the initial status to be set
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, clusterKey, clusterObj)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(clusterObj.Status.Phase).To(Equal(v1beta1.ClusterProvisioning))

				cond := meta.FindStatusCondition(clusterObj.Status.Conditions, cluster.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(cluster.ReasonProvisioning))
			}).
				WithPolling(time.Second * 2).
				WithTimeout(time.Second * 20).
				Should(Succeed())

			// Check for the status to be updated to Ready
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, clusterKey, clusterObj)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(clusterObj.Status.Phase).To(Equal(v1beta1.ClusterReady))

				cond := meta.FindStatusCondition(clusterObj.Status.Conditions, cluster.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(cluster.ReasonProvisioned))
			}).
				WithTimeout(time.Minute * 3).
				WithPolling(time.Second * 5).
				Should(Succeed())
		})

		It("created with field controlled from a policy", func() {
			ctx := context.Background()

			priorityClass := &schedv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "pc-",
				},
				Value: 100,
			}
			Expect(k8sClient.Create(ctx, priorityClass)).To(Succeed())

			clusterObj := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "status-cluster-",
					Namespace:    namespace.Name,
				},
				Spec: v1beta1.ClusterSpec{
					PriorityClass: priorityClass.Name,
				},
			}
			Expect(k8sClient.Create(ctx, clusterObj)).To(Succeed())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, priorityClass)).To(Succeed())
			})

			clusterKey := client.ObjectKeyFromObject(clusterObj)

			// Check for the initial status to be set
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, clusterKey, clusterObj)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(clusterObj.Status.Phase).To(Equal(v1beta1.ClusterProvisioning))

				cond := meta.FindStatusCondition(clusterObj.Status.Conditions, cluster.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(cluster.ReasonProvisioning))
			}).
				WithPolling(time.Second * 2).
				WithTimeout(time.Second * 20).
				Should(Succeed())

			// Check for the status to be updated to Ready
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, clusterKey, clusterObj)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(clusterObj.Status.Phase).To(Equal(v1beta1.ClusterReady))
				g.Expect(clusterObj.Status.Policy).To(Not(BeNil()))
				g.Expect(clusterObj.Status.Policy.Name).To(Equal(vcp.Name))

				cond := meta.FindStatusCondition(clusterObj.Status.Conditions, cluster.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(cluster.ReasonProvisioned))
			}).
				WithTimeout(time.Minute * 3).
				WithPolling(time.Second * 5).
				Should(Succeed())

			// update policy

			priorityClassVCP := &schedv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "pc-",
				},
				Value: 100,
			}
			Expect(k8sClient.Create(ctx, priorityClassVCP)).To(Succeed())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, priorityClassVCP)).To(Succeed())
			})

			vcp.Spec.DefaultPriorityClass = priorityClassVCP.Name
			Expect(k8sClient.Update(ctx, vcp)).To(Succeed())

			// Check for the status to be updated to Ready
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, clusterKey, clusterObj)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(clusterObj.Status.Policy).To(Not(BeNil()))
				g.Expect(clusterObj.Status.Policy.PriorityClass).To(Not(BeNil()))
				g.Expect(*clusterObj.Status.Policy.PriorityClass).To(Equal(priorityClassVCP.Name))
				g.Expect(clusterObj.Spec.PriorityClass).To(Equal(priorityClass.Name))
			}).
				WithTimeout(time.Minute * 3).
				WithPolling(time.Second * 5).
				Should(Succeed())
		})
	})

	Context("and the cluster has validation errors", func() {
		It("should be in Pending status with ValidationFailed reason", func() {
			ctx := context.Background()

			clusterObj := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "cluster-",
					Namespace:    namespace.Name,
				},
				Spec: v1beta1.ClusterSpec{
					Mode: v1beta1.VirtualClusterMode,
				},
			}
			Expect(k8sClient.Create(ctx, clusterObj)).To(Succeed())

			clusterKey := client.ObjectKeyFromObject(clusterObj)

			// Check for the status to be updated
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, clusterKey, clusterObj)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(clusterObj.Status.Phase).To(Equal(v1beta1.ClusterPending))

				cond := meta.FindStatusCondition(clusterObj.Status.Conditions, cluster.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(cluster.ReasonValidationFailed))
				g.Expect(cond.Message).To(ContainSubstring(`mode "virtual" is not allowed by the policy`))
			}).
				WithPolling(time.Second * 2).
				WithTimeout(time.Second * 20).
				Should(Succeed())
		})
	})
})
