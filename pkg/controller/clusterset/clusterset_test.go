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
			It("should have only the 'shared' allowedNodeTypes", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				allowedModeTypes := clusterSet.Spec.AllowedNodeTypes
				Expect(allowedModeTypes).To(HaveLen(1))
				Expect(allowedModeTypes).To(ContainElement(v1alpha1.SharedClusterMode))
			})

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

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
						Namespace: namespace,
					}
					return k8sClient.Get(ctx, key, clusterSetNetworkPolicy)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

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
			It("should not create a NetworkPolicy if true", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						DisableNetworkPolicy: true,
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

			It("should delete the NetworkPolicy if changed to false", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				// look for network policy
				clusterSetNetworkPolicy := &networkingv1.NetworkPolicy{}

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
						Namespace: namespace,
					}
					return k8sClient.Get(ctx, key, clusterSetNetworkPolicy)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				clusterSet.Spec.DisableNetworkPolicy = true
				err = k8sClient.Update(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				// wait for a bit for the network policy to being deleted
				Eventually(func() bool {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
						Namespace: namespace,
					}
					err := k8sClient.Get(ctx, key, clusterSetNetworkPolicy)
					return apierrors.IsNotFound(err)
				}).
					MustPassRepeatedly(5).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})

			It("should recreate the NetworkPolicy if deleted", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				// look for network policy
				clusterSetNetworkPolicy := &networkingv1.NetworkPolicy{}

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
						Namespace: namespace,
					}
					return k8sClient.Get(context.Background(), key, clusterSetNetworkPolicy)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				err = k8sClient.Delete(ctx, clusterSetNetworkPolicy)
				Expect(err).To(Not(HaveOccurred()))

				key := types.NamespacedName{
					Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
					Namespace: namespace,
				}
				err = k8sClient.Get(ctx, key, clusterSetNetworkPolicy)
				Expect(apierrors.IsNotFound(err)).Should(BeTrue())

				// wait a bit for the network policy to being recreated
				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterSet.Name),
						Namespace: namespace,
					}
					return k8sClient.Get(ctx, key, clusterSetNetworkPolicy)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeNil())
			})

		})

		When("created specifing the mode", func() {
			It("should have the 'virtual' mode if specified", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						AllowedNodeTypes: []v1alpha1.ClusterMode{
							v1alpha1.VirtualClusterMode,
						},
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				allowedModeTypes := clusterSet.Spec.AllowedNodeTypes
				Expect(allowedModeTypes).To(HaveLen(1))
				Expect(allowedModeTypes).To(ContainElement(v1alpha1.VirtualClusterMode))
			})

			It("should have both modes if specified", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						AllowedNodeTypes: []v1alpha1.ClusterMode{
							v1alpha1.SharedClusterMode,
							v1alpha1.VirtualClusterMode,
						},
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				allowedModeTypes := clusterSet.Spec.AllowedNodeTypes
				Expect(allowedModeTypes).To(HaveLen(2))
				Expect(allowedModeTypes).To(ContainElements(
					v1alpha1.SharedClusterMode,
					v1alpha1.VirtualClusterMode,
				))
			})

			It("should fail for a non-existing mode", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						AllowedNodeTypes: []v1alpha1.ClusterMode{
							v1alpha1.SharedClusterMode,
							v1alpha1.VirtualClusterMode,
							v1alpha1.ClusterMode("non-existing"),
						},
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
