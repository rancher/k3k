package policy_test

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/policy"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("VirtualClusterPolicy Controller", Label("controller"), Label("VirtualClusterPolicy"), func() {
	Context("creating a VirtualClusterPolicy", func() {
		It("should have the 'shared' allowedMode", func() {
			policy := newPolicy(v1beta1.VirtualClusterPolicySpec{})
			Expect(policy.Spec.AllowedMode).To(Equal(v1beta1.SharedClusterMode))
		})

		It("should have the 'virtual' mode if specified", func() {
			policy := newPolicy(v1beta1.VirtualClusterPolicySpec{
				AllowedMode: v1beta1.VirtualClusterMode,
			})

			Expect(policy.Spec.AllowedMode).To(Equal(v1beta1.VirtualClusterMode))
		})

		It("should fail for a non-existing mode", func() {
			policy := &v1beta1.VirtualClusterPolicy{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "policy-",
				},
				Spec: v1beta1.VirtualClusterPolicySpec{
					AllowedMode: v1beta1.ClusterMode("non-existing"),
				},
			}

			err := k8sClient.Create(ctx, policy)
			Expect(err).To(HaveOccurred())
		})

		When("bound to a namespace", func() {
			var namespace *v1.Namespace

			BeforeEach(func() {
				namespace = &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "ns-",
					},
				}

				err := k8sClient.Create(ctx, namespace)
				Expect(err).To(Not(HaveOccurred()))
			})

			It("should create a NetworkPolicy", func() {
				policy := newPolicy(v1beta1.VirtualClusterPolicySpec{})
				bindPolicyToNamespace(namespace, policy)

				// look for network policies etc
				networkPolicy := &networkingv1.NetworkPolicy{}

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace.Name,
					}
					return k8sClient.Get(ctx, key, networkPolicy)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				spec := networkPolicy.Spec
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
				namespaceRule := networkingv1.NetworkPolicyPeer{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": namespace.Name},
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
					ipBlockRule, namespaceRule, kubeDNSRule,
				))
			})

			It("should recreate the NetworkPolicy if deleted", func() {
				policy := newPolicy(v1beta1.VirtualClusterPolicySpec{})
				bindPolicyToNamespace(namespace, policy)

				// look for network policy
				networkPolicy := &networkingv1.NetworkPolicy{}

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace.Name,
					}
					return k8sClient.Get(context.Background(), key, networkPolicy)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				err := k8sClient.Delete(ctx, networkPolicy)
				Expect(err).To(Not(HaveOccurred()))

				key := types.NamespacedName{
					Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
					Namespace: namespace.Name,
				}
				err = k8sClient.Get(ctx, key, networkPolicy)
				Expect(apierrors.IsNotFound(err)).Should(BeTrue())

				// wait a bit for the network policy to being recreated
				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace.Name,
					}
					return k8sClient.Get(ctx, key, networkPolicy)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeNil())
			})

			It("should add and update the proper pod-security labels to the namespace", func() {
				var (
					privileged = v1beta1.PrivilegedPodSecurityAdmissionLevel
					baseline   = v1beta1.BaselinePodSecurityAdmissionLevel
					restricted = v1beta1.RestrictedPodSecurityAdmissionLevel
				)

				policy := newPolicy(v1beta1.VirtualClusterPolicySpec{
					PodSecurityAdmissionLevel: &privileged,
				})

				bindPolicyToNamespace(namespace, policy)

				var ns v1.Namespace

				// Check privileged

				// wait a bit for the namespace to be updated
				Eventually(func() string {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: namespace.Name}, &ns)
					Expect(err).To(Not(HaveOccurred()))
					return ns.Labels["pod-security.kubernetes.io/enforce"]
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(Equal("privileged"))

				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "privileged"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce-version", "latest"))
				Expect(ns.Labels).Should(Not(HaveKey("pod-security.kubernetes.io/warn")))
				Expect(ns.Labels).Should(Not(HaveKey("pod-security.kubernetes.io/warn-version")))

				// Check baseline

				// get policy again
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)
				Expect(err).To(Not(HaveOccurred()))

				policy.Spec.PodSecurityAdmissionLevel = &baseline
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit for the namespace to be updated
				Eventually(func() string {
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace.Name}, &ns)
					Expect(err).To(Not(HaveOccurred()))
					return ns.Labels["pod-security.kubernetes.io/enforce"]
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(Equal("baseline"))

				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "baseline"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce-version", "latest"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/warn", "baseline"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/warn-version", "latest"))

				// Check restricted

				policy.Spec.PodSecurityAdmissionLevel = &restricted
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit for the namespace to be updated
				Eventually(func() string {
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace.Name}, &ns)
					Expect(err).To(Not(HaveOccurred()))
					return ns.Labels["pod-security.kubernetes.io/enforce"]
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(Equal("restricted"))

				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "restricted"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce-version", "latest"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/warn", "restricted"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/warn-version", "latest"))

				// check cleanup

				policy.Spec.PodSecurityAdmissionLevel = nil
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit for the namespace to be updated
				Eventually(func() bool {
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace.Name}, &ns)
					Expect(err).To(Not(HaveOccurred()))
					_, found := ns.Labels["pod-security.kubernetes.io/enforce"]
					return found
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeFalse())

				Expect(ns.Labels).Should(Not(HaveKey("pod-security.kubernetes.io/enforce")))
				Expect(ns.Labels).Should(Not(HaveKey("pod-security.kubernetes.io/enforce-version")))
				Expect(ns.Labels).Should(Not(HaveKey("pod-security.kubernetes.io/warn")))
				Expect(ns.Labels).Should(Not(HaveKey("pod-security.kubernetes.io/warn-version")))
			})

			It("should restore the labels if Namespace is updated", func() {
				privileged := v1beta1.PrivilegedPodSecurityAdmissionLevel

				policy := newPolicy(v1beta1.VirtualClusterPolicySpec{
					PodSecurityAdmissionLevel: &privileged,
				})

				bindPolicyToNamespace(namespace, policy)

				var ns v1.Namespace

				// wait a bit for the namespace to be updated
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: namespace.Name}, &ns)
					Expect(err).To(Not(HaveOccurred()))
					enforceValue := ns.Labels["pod-security.kubernetes.io/enforce"]
					return enforceValue == "privileged"
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())

				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "privileged"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce-version", "latest"))

				ns.Labels["pod-security.kubernetes.io/enforce"] = "baseline"
				err := k8sClient.Update(ctx, &ns)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit for the namespace to be restored
				Eventually(func() bool {
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace.Name}, &ns)
					Expect(err).To(Not(HaveOccurred()))
					enforceValue := ns.Labels["pod-security.kubernetes.io/enforce"]
					return enforceValue == "privileged"
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())

				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "privileged"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce-version", "latest"))
			})

			It("updates the Cluster's policy status with the DefaultPriorityClass", func() {
				policy := newPolicy(v1beta1.VirtualClusterPolicySpec{
					DefaultPriorityClass: "foobar",
				})

				bindPolicyToNamespace(namespace, policy)

				cluster := &v1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace.Name,
					},
					Spec: v1beta1.ClusterSpec{
						Mode:    v1beta1.SharedClusterMode,
						Servers: ptr.To[int32](1),
						Agents:  ptr.To[int32](0),
					},
				}

				err := k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				Eventually(func(g Gomega) {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					g.Expect(err).To(Not(HaveOccurred()))

					g.Expect(cluster.Spec.PriorityClass).To(BeEmpty())
					g.Expect(cluster.Status.Policy).To(Not(BeNil()))
					g.Expect(cluster.Status.Policy.PriorityClass).To(Not(BeNil()))
					g.Expect(*cluster.Status.Policy.PriorityClass).To(Equal(policy.Spec.DefaultPriorityClass))
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(Succeed())
			})

			It("updates the Cluster's policy status with the DefaultNodeSelector", func() {
				policy := newPolicy(v1beta1.VirtualClusterPolicySpec{
					DefaultNodeSelector: map[string]string{"label-1": "value-1"},
				})
				bindPolicyToNamespace(namespace, policy)

				err := k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				cluster := &v1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace.Name,
					},
					Spec: v1beta1.ClusterSpec{
						Mode:    v1beta1.SharedClusterMode,
						Servers: ptr.To[int32](1),
						Agents:  ptr.To[int32](0),
					},
				}

				err = k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit
				Eventually(func(g Gomega) {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					Expect(err).To(Not(HaveOccurred()))

					g.Expect(cluster.Spec.NodeSelector).To(BeEmpty())
					g.Expect(cluster.Status.Policy).To(Not(BeNil()))
					g.Expect(cluster.Status.Policy.NodeSelector).To(Equal(map[string]string{"label-1": "value-1"}))
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(Succeed())
			})

			It("updates the Cluster's policy status when the VCP nodeSelector changes", func() {
				policy := newPolicy(v1beta1.VirtualClusterPolicySpec{
					DefaultNodeSelector: map[string]string{"label-1": "value-1"},
				})
				bindPolicyToNamespace(namespace, policy)

				cluster := &v1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace.Name,
					},
					Spec: v1beta1.ClusterSpec{
						Mode:         v1beta1.SharedClusterMode,
						Servers:      ptr.To[int32](1),
						Agents:       ptr.To[int32](0),
						NodeSelector: map[string]string{"label-1": "value-1"},
					},
				}

				err := k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				// Cluster Spec should not change, VCP NodeSelector should be present in the Status
				Eventually(func(g Gomega) {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					Expect(err).To(Not(HaveOccurred()))

					g.Expect(cluster.Spec.NodeSelector).To(Equal(map[string]string{"label-1": "value-1"}))
					g.Expect(cluster.Status.Policy).To(Not(BeNil()))
					g.Expect(cluster.Status.Policy.NodeSelector).To(Equal(map[string]string{"label-1": "value-1"}))
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(Succeed())

				// update the VirtualClusterPolicy
				policy.Spec.DefaultNodeSelector["label-2"] = "value-2"
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit
				Eventually(func(g Gomega) {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					Expect(err).To(Not(HaveOccurred()))

					g.Expect(cluster.Spec.NodeSelector).To(Equal(map[string]string{"label-1": "value-1"}))
					g.Expect(cluster.Status.Policy).To(Not(BeNil()))
					g.Expect(cluster.Status.Policy.NodeSelector).To(Equal(map[string]string{"label-1": "value-1", "label-2": "value-2"}))
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(Succeed())

				// Update the Cluster
				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)
				Expect(err).To(Not(HaveOccurred()))
				cluster.Spec.NodeSelector["label-3"] = "value-3"
				err = k8sClient.Update(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit and check it's restored
				Consistently(func(g Gomega) {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					g.Expect(err).To(Not(HaveOccurred()))
					g.Expect(cluster.Spec.NodeSelector).To(Equal(map[string]string{"label-1": "value-1", "label-3": "value-3"}))
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(Succeed())
			})

			It("should create a ResourceQuota if Quota is enabled", func() {
				policy := newPolicy(v1beta1.VirtualClusterPolicySpec{
					Quota: &v1.ResourceQuotaSpec{
						Hard: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("800m"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				})

				bindPolicyToNamespace(namespace, policy)

				var resourceQuota v1.ResourceQuota
				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace.Name,
					}

					return k8sClient.Get(ctx, key, &resourceQuota)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeNil())
				Expect(resourceQuota.Spec.Hard.Cpu().String()).To(BeEquivalentTo("800m"))
				Expect(resourceQuota.Spec.Hard.Memory().String()).To(BeEquivalentTo("1Gi"))
			})

			It("should delete the ResourceQuota if Quota is deleted", func() {
				policy := newPolicy(v1beta1.VirtualClusterPolicySpec{
					Quota: &v1.ResourceQuotaSpec{
						Hard: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("800m"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				})

				bindPolicyToNamespace(namespace, policy)

				var resourceQuota v1.ResourceQuota

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace.Name,
					}
					return k8sClient.Get(ctx, key, &resourceQuota)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				// get policy again
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)
				Expect(err).To(Not(HaveOccurred()))
				policy.Spec.Quota = nil
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait for a bit for the resourceQuota to be deleted
				Eventually(func() bool {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace.Name,
					}
					err := k8sClient.Get(ctx, key, &resourceQuota)
					return apierrors.IsNotFound(err)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})

			It("should delete the ResourceQuota if unbound", func() {
				clusterPolicy := newPolicy(v1beta1.VirtualClusterPolicySpec{
					Quota: &v1.ResourceQuotaSpec{
						Hard: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("800m"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				})

				bindPolicyToNamespace(namespace, clusterPolicy)

				var resourceQuota v1.ResourceQuota

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterPolicy.Name),
						Namespace: namespace.Name,
					}
					return k8sClient.Get(ctx, key, &resourceQuota)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				delete(namespace.Labels, policy.PolicyNameLabelKey)
				err := k8sClient.Update(ctx, namespace)
				Expect(err).To(Not(HaveOccurred()))

				// wait for a bit for the resourceQuota to be deleted
				Eventually(func() bool {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(clusterPolicy.Name),
						Namespace: namespace.Name,
					}
					err := k8sClient.Get(ctx, key, &resourceQuota)
					return apierrors.IsNotFound(err)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})
		})
	})
})

func newPolicy(spec v1beta1.VirtualClusterPolicySpec) *v1beta1.VirtualClusterPolicy {
	GinkgoHelper()

	policy := &v1beta1.VirtualClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "policy-",
		},
		Spec: spec,
	}

	err := k8sClient.Create(ctx, policy)
	Expect(err).To(Not(HaveOccurred()))

	return policy
}

func bindPolicyToNamespace(namespace *v1.Namespace, pol *v1beta1.VirtualClusterPolicy) {
	GinkgoHelper()

	if len(namespace.Labels) == 0 {
		namespace.Labels = map[string]string{}
	}

	namespace.Labels[policy.PolicyNameLabelKey] = pol.Name

	err := k8sClient.Update(ctx, namespace)
	Expect(err).To(Not(HaveOccurred()))
}
