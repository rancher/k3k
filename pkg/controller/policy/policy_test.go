package policy_test

import (
	"context"
	"reflect"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("VirtualClusterPolicy Controller", Label("controller"), Label("VirtualClusterPolicy"), func() {

	Context("creating a VirtualClusterPolicy", func() {

		var (
			namespace string
		)

		BeforeEach(func() {
			createdNS := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"}}
			err := k8sClient.Create(context.Background(), createdNS)
			Expect(err).To(Not(HaveOccurred()))
			namespace = createdNS.Name
		})

		When("created with a default spec", func() {
			It("should have only the 'shared' allowedModeTypes", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				allowedModeTypes := policy.Spec.AllowedModeTypes
				Expect(allowedModeTypes).To(HaveLen(1))
				Expect(allowedModeTypes).To(ContainElement(v1alpha1.SharedClusterMode))
			})

			It("should not be able to create a cluster with a non 'default' name", func() {
				err := k8sClient.Create(ctx, &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "another-name",
						Namespace: namespace,
					},
				})
				Expect(err).To(HaveOccurred())
			})

			It("should not be able to create two VirtualClusterPolicys in the same namespace", func() {
				err := k8sClient.Create(ctx, &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
				})
				Expect(err).To(Not(HaveOccurred()))

				err = k8sClient.Create(ctx, &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default-2",
						Namespace: namespace,
					},
				})
				Expect(err).To(HaveOccurred())
			})

			It("should create a NetworkPolicy", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// look for network policies etc
				networkPolicy := &networkingv1.NetworkPolicy{}

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace,
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
					ipBlockRule, namespaceRule, kubeDNSRule,
				))
			})
		})

		When("created with DisableNetworkPolicy", func() {
			It("should not create a NetworkPolicy if true", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						DisableNetworkPolicy: true,
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait for a bit for the network policy, but it should not be created
				Eventually(func() bool {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
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
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// look for network policy
				networkPolicy := &networkingv1.NetworkPolicy{}

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace,
					}
					return k8sClient.Get(ctx, key, networkPolicy)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				policy.Spec.DisableNetworkPolicy = true
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait for a bit for the network policy to being deleted
				Eventually(func() bool {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace,
					}
					err := k8sClient.Get(ctx, key, networkPolicy)
					return apierrors.IsNotFound(err)
				}).
					MustPassRepeatedly(5).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})

			It("should recreate the NetworkPolicy if deleted", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// look for network policy
				networkPolicy := &networkingv1.NetworkPolicy{}

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace,
					}
					return k8sClient.Get(context.Background(), key, networkPolicy)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				err = k8sClient.Delete(ctx, networkPolicy)
				Expect(err).To(Not(HaveOccurred()))

				key := types.NamespacedName{
					Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
					Namespace: namespace,
				}
				err = k8sClient.Get(ctx, key, networkPolicy)
				Expect(apierrors.IsNotFound(err)).Should(BeTrue())

				// wait a bit for the network policy to being recreated
				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace,
					}
					return k8sClient.Get(ctx, key, networkPolicy)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeNil())
			})

		})

		When("created specifying the mode", func() {
			It("should have the 'virtual' mode if specified", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						AllowedModeTypes: []v1alpha1.ClusterMode{
							v1alpha1.VirtualClusterMode,
						},
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				allowedModeTypes := policy.Spec.AllowedModeTypes
				Expect(allowedModeTypes).To(HaveLen(1))
				Expect(allowedModeTypes).To(ContainElement(v1alpha1.VirtualClusterMode))
			})

			It("should have both modes if specified", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						AllowedModeTypes: []v1alpha1.ClusterMode{
							v1alpha1.SharedClusterMode,
							v1alpha1.VirtualClusterMode,
						},
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				allowedModeTypes := policy.Spec.AllowedModeTypes
				Expect(allowedModeTypes).To(HaveLen(2))
				Expect(allowedModeTypes).To(ContainElements(
					v1alpha1.SharedClusterMode,
					v1alpha1.VirtualClusterMode,
				))
			})

			It("should fail for a non-existing mode", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						AllowedModeTypes: []v1alpha1.ClusterMode{
							v1alpha1.SharedClusterMode,
							v1alpha1.VirtualClusterMode,
							v1alpha1.ClusterMode("non-existing"),
						},
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(HaveOccurred())
			})
		})

		When("created specifying the podSecurityAdmissionLevel", func() {
			It("should add and update the proper pod-security labels to the namespace", func() {
				var (
					privileged = v1alpha1.PrivilegedPodSecurityAdmissionLevel
					baseline   = v1alpha1.BaselinePodSecurityAdmissionLevel
					restricted = v1alpha1.RestrictedPodSecurityAdmissionLevel
				)

				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						PodSecurityAdmissionLevel: &privileged,
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				var ns v1.Namespace

				// Check privileged

				// wait a bit for the namespace to be updated
				Eventually(func() bool {
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, &ns)
					Expect(err).To(Not(HaveOccurred()))
					enforceValue := ns.Labels["pod-security.kubernetes.io/enforce"]
					return enforceValue == "privileged"
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())

				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "privileged"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce-version", "latest"))
				Expect(ns.Labels).Should(Not(HaveKey("pod-security.kubernetes.io/warn")))
				Expect(ns.Labels).Should(Not(HaveKey("pod-security.kubernetes.io/warn-version")))

				// Check baseline

				policy.Spec.PodSecurityAdmissionLevel = &baseline
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit for the namespace to be updated
				Eventually(func() bool {
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, &ns)
					Expect(err).To(Not(HaveOccurred()))
					enforceValue := ns.Labels["pod-security.kubernetes.io/enforce"]
					return enforceValue == "baseline"
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())

				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "baseline"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/enforce-version", "latest"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/warn", "baseline"))
				Expect(ns.Labels).Should(HaveKeyWithValue("pod-security.kubernetes.io/warn-version", "latest"))

				// Check restricted

				policy.Spec.PodSecurityAdmissionLevel = &restricted
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit for the namespace to be updated
				Eventually(func() bool {
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, &ns)
					Expect(err).To(Not(HaveOccurred()))
					enforceValue := ns.Labels["pod-security.kubernetes.io/enforce"]
					return enforceValue == "restricted"
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())

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
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, &ns)
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
				privileged := v1alpha1.PrivilegedPodSecurityAdmissionLevel

				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						PodSecurityAdmissionLevel: &privileged,
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				var ns v1.Namespace

				// wait a bit for the namespace to be updated
				Eventually(func() bool {
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, &ns)
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
				err = k8sClient.Update(ctx, &ns)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit for the namespace to be restored
				Eventually(func() bool {
					err = k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, &ns)
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
		})

		When("a cluster in the same namespace is present", func() {
			It("should update it if needed", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						DefaultPriorityClass: "foobar",
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				cluster := &v1alpha1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSpec{
						Mode:    v1alpha1.SharedClusterMode,
						Servers: ptr.To[int32](1),
						Agents:  ptr.To[int32](0),
					},
				}

				err = k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit
				Eventually(func() bool {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					Expect(err).To(Not(HaveOccurred()))
					return cluster.Spec.PriorityClass == policy.Spec.DefaultPriorityClass
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})

			It("should update the nodeSelector", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						DefaultNodeSelector: map[string]string{"label-1": "value-1"},
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				cluster := &v1alpha1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSpec{
						Mode:    v1alpha1.SharedClusterMode,
						Servers: ptr.To[int32](1),
						Agents:  ptr.To[int32](0),
					},
				}

				err = k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				// wait a bit
				Eventually(func() bool {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					Expect(err).To(Not(HaveOccurred()))
					return reflect.DeepEqual(cluster.Spec.NodeSelector, policy.Spec.DefaultNodeSelector)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})

			It("should update the nodeSelector if changed", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						DefaultNodeSelector: map[string]string{"label-1": "value-1"},
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				cluster := &v1alpha1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSpec{
						Mode:         v1alpha1.SharedClusterMode,
						Servers:      ptr.To[int32](1),
						Agents:       ptr.To[int32](0),
						NodeSelector: map[string]string{"label-1": "value-1"},
					},
				}

				err = k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				Expect(cluster.Spec.NodeSelector).To(Equal(policy.Spec.DefaultNodeSelector))

				// update the VirtualClusterPolicy
				policy.Spec.DefaultNodeSelector["label-2"] = "value-2"
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))
				Expect(cluster.Spec.NodeSelector).To(Not(Equal(policy.Spec.DefaultNodeSelector)))

				// wait a bit
				Eventually(func() bool {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					Expect(err).To(Not(HaveOccurred()))
					return reflect.DeepEqual(cluster.Spec.NodeSelector, policy.Spec.DefaultNodeSelector)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())

				// Update the Cluster
				cluster.Spec.NodeSelector["label-3"] = "value-3"
				err = k8sClient.Update(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))
				Expect(cluster.Spec.NodeSelector).To(Not(Equal(policy.Spec.DefaultNodeSelector)))

				// wait a bit and check it's restored
				Eventually(func() bool {
					var updatedCluster v1alpha1.Cluster

					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, &updatedCluster)
					Expect(err).To(Not(HaveOccurred()))
					return reflect.DeepEqual(updatedCluster.Spec.NodeSelector, policy.Spec.DefaultNodeSelector)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})
		})

		When("a cluster in a different namespace is present", func() {
			It("should not be update", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						DefaultPriorityClass: "foobar",
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				namespace2 := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"}}
				err = k8sClient.Create(ctx, namespace2)
				Expect(err).To(Not(HaveOccurred()))

				cluster := &v1alpha1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace2.Name,
					},
					Spec: v1alpha1.ClusterSpec{
						Mode:    v1alpha1.SharedClusterMode,
						Servers: ptr.To[int32](1),
						Agents:  ptr.To[int32](0),
					},
				}

				err = k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				// it should not change!
				Eventually(func() bool {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					Expect(err).To(Not(HaveOccurred()))
					return cluster.Spec.PriorityClass != policy.Spec.DefaultPriorityClass
				}).
					MustPassRepeatedly(5).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})
		})

		When("created with ResourceQuota", func() {
			It("should create resourceQuota if Quota is enabled", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						Quota: &v1.ResourceQuotaSpec{
							Hard: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("800m"),
								v1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				var resourceQuota v1.ResourceQuota
				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace,
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
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						Quota: &v1.ResourceQuotaSpec{
							Hard: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("800m"),
								v1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				var resourceQuota v1.ResourceQuota

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace,
					}
					return k8sClient.Get(ctx, key, &resourceQuota)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				policy.Spec.Quota = nil
				err = k8sClient.Update(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				// wait for a bit for the resourceQuota to be deleted
				Eventually(func() bool {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace,
					}
					err := k8sClient.Get(ctx, key, &resourceQuota)
					return apierrors.IsNotFound(err)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})

			It("should create resourceQuota if Quota is enabled", func() {
				policy := &v1alpha1.VirtualClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: namespace,
					},
					Spec: v1alpha1.VirtualClusterPolicySpec{
						Limit: &v1.LimitRangeSpec{
							Limits: []v1.LimitRangeItem{
								{
									Type: v1.LimitTypeContainer,
									DefaultRequest: v1.ResourceList{
										v1.ResourceCPU: resource.MustParse("500m"),
									},
								},
							},
						},
					},
				}

				err := k8sClient.Create(ctx, policy)
				Expect(err).To(Not(HaveOccurred()))

				var limitRange v1.LimitRange

				Eventually(func() error {
					key := types.NamespacedName{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(policy.Name),
						Namespace: namespace,
					}
					return k8sClient.Get(ctx, key, &limitRange)
				}).
					WithTimeout(time.Minute).
					WithPolling(time.Second).
					Should(BeNil())

				// make sure that default limit range has the default requet values.
				Expect(limitRange.Spec.Limits).ShouldNot(BeEmpty())
				cpu := limitRange.Spec.Limits[0].DefaultRequest.Cpu().String()
				Expect(cpu).To(BeEquivalentTo("500m"))
			})
		})
	})
})
