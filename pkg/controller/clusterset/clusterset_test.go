package clusterset_test

import (
	"context"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ClusterSet Controller", func() {

	Context("creating a ClusterSet", func() {

		var (
			namespace string
		)

		BeforeEach(func() {
			createdNS := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{GenerateName: "ns-"}}
			err := k8sClient.Create(context.Background(), createdNS)
			Expect(err).To(Not(HaveOccurred()))
			namespace = createdNS.Name
		})

		When("created with a default spec", func() {
			It("should create a NetworkPolicy", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				// look for network policies etc
				clusterSetNetworkPolicy := &networkingv1.NetworkPolicy{}

				Eventually(func() bool {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
						Namespace: namespace,
					}
					err := k8sClient.Get(ctx, key, clusterSetNetworkPolicy)
					return err == nil
				}, time.Minute, time.Second).Should(BeTrue())

				spec := clusterSetNetworkPolicy.Spec
				Expect(spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeEgress))
				Expect(spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))

				// ingress should allow everything
				Expect(spec.Ingress).To(ConsistOf(networkingv1.NetworkPolicyIngressRule{}))

				// egress should contains some rules
				Expect(spec.Egress).To(HaveLen(1))

				// allow networking to all external IPs
				ipBlockRule := networkingv1.NetworkPolicyPeer{
					IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0"},
				}

				// allow networking in the same namespace
				clusterSetNamespaceRule := networkingv1.NetworkPolicyPeer{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": namespace},
					},
				}

				// allow networking to the "kube-dns" pod in the "kube-system" namespace
				kubeDNSRule := networkingv1.NetworkPolicyPeer{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"k8s-app": "kube-dns"},
					},
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
					},
				}

				Expect(spec.Egress[0].To).To(ContainElements(
					ipBlockRule, clusterSetNamespaceRule, kubeDNSRule,
				))
			})
		})

		When("created with DisableNetworkPolicy", func() {
			It("should not create a NetworkPolicy", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						DisableNetworkPolicy: true,
						Mode:                 v1alpha1.Virtual,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				// wait for a bit for the network policy, but it should not be created
				Eventually(func() bool {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
						Namespace: namespace,
					}
					err := k8sClient.Get(ctx, key, &networkingv1.NetworkPolicy{})
					return apierrors.IsNotFound(err)
				}).
					MustPassRepeatedly(5).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})
		})

		When("created specifing the mode", func() {
			It("should create a shared mode by default", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))
				Expect(clusterSet.Spec.Mode).To(Equal(v1alpha1.Shared))
			})

			It("should create a virtual mode if specified", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						Mode: v1alpha1.Virtual,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))
				Expect(clusterSet.Spec.Mode).To(Equal(v1alpha1.Virtual))
			})

			It("should fail for a non-existing mode", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						Mode: "foobar",
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(HaveOccurred())
			})
		})

		When("a ClusterSet in the same namespace was already there", func() {
			It("should delete the last one", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				clusterSet2 := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
				}

				err = k8sClient.Create(ctx, clusterSet2)
				Expect(err).To(Not(HaveOccurred()))

				Eventually(func() bool {
					namespacedKey := types.NamespacedName{
						Name:      clusterSet2.Name,
						Namespace: namespace,
					}

					var deletedClusterSet v1alpha1.ClusterSet
					err = k8sClient.Get(ctx, namespacedKey, &deletedClusterSet)

					return apierrors.IsNotFound(err)
				}, time.Minute, time.Second).Should(BeTrue())
			})
		})
	})
})
