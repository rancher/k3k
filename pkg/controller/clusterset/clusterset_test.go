package clusterset_test

import (
	"context"
	"reflect"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ClusterSet Controller", Label("controller"), Label("ClusterSet"), func() {

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
			It("should have only the 'shared' allowedModeTypes", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				allowedModeTypes := clusterSet.Spec.AllowedModeTypes
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

		When("created specifying the mode", func() {
			It("should have the 'virtual' mode if specified", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						AllowedModeTypes: []v1alpha1.ClusterMode{
							v1alpha1.VirtualClusterMode,
						},
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				allowedModeTypes := clusterSet.Spec.AllowedModeTypes
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
						AllowedModeTypes: []v1alpha1.ClusterMode{
							v1alpha1.SharedClusterMode,
							v1alpha1.VirtualClusterMode,
						},
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				allowedModeTypes := clusterSet.Spec.AllowedModeTypes
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
						AllowedModeTypes: []v1alpha1.ClusterMode{
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

		When("created specifying the podSecurityAdmissionLevel", func() {
			It("should add and update the proper pod-security labels to the namespace", func() {
				var (
					privileged = v1alpha1.PrivilegedPodSecurityAdmissionLevel
					baseline   = v1alpha1.BaselinePodSecurityAdmissionLevel
					restricted = v1alpha1.RestrictedPodSecurityAdmissionLevel
				)

				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						PodSecurityAdmissionLevel: &privileged,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				var ns corev1.Namespace

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

				clusterSet.Spec.PodSecurityAdmissionLevel = &baseline
				err = k8sClient.Update(ctx, clusterSet)
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

				clusterSet.Spec.PodSecurityAdmissionLevel = &restricted
				err = k8sClient.Update(ctx, clusterSet)
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

				clusterSet.Spec.PodSecurityAdmissionLevel = nil
				err = k8sClient.Update(ctx, clusterSet)
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

				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						PodSecurityAdmissionLevel: &privileged,
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				var ns corev1.Namespace

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
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						DefaultPriorityClass: "foobar",
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				cluster := &v1alpha1.Cluster{
					ObjectMeta: v1.ObjectMeta{
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
					return cluster.Spec.PriorityClass == clusterSet.Spec.DefaultPriorityClass
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})

			It("should update the nodeSelector", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						DefaultNodeSelector: map[string]string{"label-1": "value-1"},
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				cluster := &v1alpha1.Cluster{
					ObjectMeta: v1.ObjectMeta{
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
					return reflect.DeepEqual(cluster.Spec.NodeSelector, clusterSet.Spec.DefaultNodeSelector)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})

			It("should update the nodeSelector if changed", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						DefaultNodeSelector: map[string]string{"label-1": "value-1"},
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				cluster := &v1alpha1.Cluster{
					ObjectMeta: v1.ObjectMeta{
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

				Expect(cluster.Spec.NodeSelector).To(Equal(clusterSet.Spec.DefaultNodeSelector))

				// update the ClusterSet
				clusterSet.Spec.DefaultNodeSelector["label-2"] = "value-2"
				err = k8sClient.Update(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))
				Expect(cluster.Spec.NodeSelector).To(Not(Equal(clusterSet.Spec.DefaultNodeSelector)))

				// wait a bit
				Eventually(func() bool {
					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, cluster)
					Expect(err).To(Not(HaveOccurred()))
					return reflect.DeepEqual(cluster.Spec.NodeSelector, clusterSet.Spec.DefaultNodeSelector)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())

				// Update the Cluster
				cluster.Spec.NodeSelector["label-3"] = "value-3"
				err = k8sClient.Update(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))
				Expect(cluster.Spec.NodeSelector).To(Not(Equal(clusterSet.Spec.DefaultNodeSelector)))

				// wait a bit and check it's restored
				Eventually(func() bool {
					var updatedCluster v1alpha1.Cluster

					key := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
					err = k8sClient.Get(ctx, key, &updatedCluster)
					Expect(err).To(Not(HaveOccurred()))
					return reflect.DeepEqual(updatedCluster.Spec.NodeSelector, clusterSet.Spec.DefaultNodeSelector)
				}).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})
		})

		When("a cluster in a different namespace is present", func() {
			It("should not be update", func() {
				clusterSet := &v1alpha1.ClusterSet{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
					Spec: v1alpha1.ClusterSetSpec{
						DefaultPriorityClass: "foobar",
					},
				}

				err := k8sClient.Create(ctx, clusterSet)
				Expect(err).To(Not(HaveOccurred()))

				namespace2 := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{GenerateName: "ns-"}}
				err = k8sClient.Create(ctx, namespace2)
				Expect(err).To(Not(HaveOccurred()))

				cluster := &v1alpha1.Cluster{
					ObjectMeta: v1.ObjectMeta{
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
					return cluster.Spec.PriorityClass != clusterSet.Spec.DefaultPriorityClass
				}).
					MustPassRepeatedly(5).
					WithTimeout(time.Second * 10).
					WithPolling(time.Second).
					Should(BeTrue())
			})
		})
	})
})
