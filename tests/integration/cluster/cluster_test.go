package cluster_test

import (
	"context"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/server"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cluster Controller", Label("controller"), Label("Cluster"), func() {
	Context("creating a Cluster", func() {
		var (
			namespace string
			ctx       context.Context
		)

		BeforeEach(func() {
			ctx = context.Background()

			createdNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"}}
			err := k8sClient.Create(context.Background(), createdNS)
			Expect(err).To(Not(HaveOccurred()))

			namespace = createdNS.Name
		})

		When("creating a Cluster", func() {
			It("will be created with some defaults", func() {
				cluster := &v1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				Expect(cluster.Spec.Mode).To(Equal(v1beta1.SharedClusterMode))
				Expect(cluster.Spec.Agents).To(Equal(ptr.To[int32](0)))
				Expect(cluster.Spec.Servers).To(Equal(ptr.To[int32](1)))
				Expect(cluster.Spec.Version).To(BeEmpty())

				Expect(cluster.Spec.CustomCAs).To(BeNil())

				// sync
				// enabled by default
				Expect(cluster.Spec.Sync).To(Not(BeNil()))
				Expect(cluster.Spec.Sync.ConfigMaps.Enabled).To(BeTrue())
				Expect(cluster.Spec.Sync.PersistentVolumeClaims.Enabled).To(BeTrue())
				Expect(cluster.Spec.Sync.Secrets.Enabled).To(BeTrue())
				Expect(cluster.Spec.Sync.Services.Enabled).To(BeTrue())
				// disabled by default
				Expect(cluster.Spec.Sync.Ingresses.Enabled).To(BeFalse())
				Expect(cluster.Spec.Sync.PriorityClasses.Enabled).To(BeFalse())

				Expect(cluster.Spec.Persistence.Type).To(Equal(v1beta1.DynamicPersistenceMode))
				Expect(cluster.Spec.Persistence.StorageRequestSize.Equal(resource.MustParse("2G"))).To(BeTrue())

				Expect(cluster.Status.Phase).To(Equal(v1beta1.ClusterUnknown))

				serverVersion, err := k8s.ServerVersion()
				Expect(err).To(Not(HaveOccurred()))

				Eventually(func() string {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)
					Expect(err).To(Not(HaveOccurred()))

					return cluster.Status.HostVersion
				}).
					WithTimeout(time.Second * 30).
					WithPolling(time.Second).
					Should(Equal(serverVersion.GitVersion))

				// check NetworkPolicy
				expectedNetworkPolicy := &networkingv1.NetworkPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(cluster.Name),
						Namespace: cluster.Namespace,
					},
				}

				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(expectedNetworkPolicy), expectedNetworkPolicy)
				Expect(err).To(Not(HaveOccurred()))

				spec := expectedNetworkPolicy.Spec
				Expect(spec.PolicyTypes).To(HaveLen(2))
				Expect(spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeEgress))
				Expect(spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))

				Expect(spec.Ingress).To(Equal([]networkingv1.NetworkPolicyIngressRule{{}}))

				// the policy should only select synced workload pods, leaving the k3k infra
				// pods (kubelet, server) unrestricted so they can reach the host API server
				Expect(spec.PodSelector.MatchExpressions).To(ConsistOf(metav1.LabelSelectorRequirement{
					Key:      translate.ClusterNameLabel,
					Operator: metav1.LabelSelectorOpExists,
				}))
			})

			When("a host Node advertises a non-default PodCIDR", func() {
				It("excludes the real PodCIDR from the NetworkPolicy egress, not a hardcoded guess", func() {
					node := &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{GenerateName: "node-"},
						Spec:       corev1.NodeSpec{PodCIDR: "192.168.77.0/24"},
					}

					Expect(k8sClient.Create(ctx, node)).To(Succeed())
					DeferCleanup(func() {
						Expect(k8sClient.Delete(context.Background(), node)).To(Succeed())
					})

					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					expectedNetworkPolicy := &networkingv1.NetworkPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name:      k3kcontroller.SafeConcatNameWithPrefix(cluster.Name),
							Namespace: cluster.Namespace,
						},
					}

					Eventually(func(g Gomega) {
						err := k8sClient.Get(ctx, client.ObjectKeyFromObject(expectedNetworkPolicy), expectedNetworkPolicy)
						g.Expect(err).To(Not(HaveOccurred()))

						egressPeers := expectedNetworkPolicy.Spec.Egress[0].To
						g.Expect(egressPeers).To(ContainElement(networkingv1.NetworkPolicyPeer{
							IPBlock: &networkingv1.IPBlock{
								CIDR:   "0.0.0.0/0",
								Except: []string{"192.168.77.0/24"},
							},
						}))
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Succeed())
				})
			})

			When("exposing the cluster with nodePort", func() {
				It("will have a NodePort service", func() {
					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1beta1.ClusterSpec{
							Expose: &v1beta1.ExposeConfig{
								NodePort: &v1beta1.NodePortConfig{},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					var service corev1.Service

					Eventually(func() corev1.ServiceType {
						serviceKey := client.ObjectKey{
							Name:      server.ServiceName(cluster.Name),
							Namespace: cluster.Namespace,
						}

						err := k8sClient.Get(ctx, serviceKey, &service)
						Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))

						return service.Spec.Type
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Equal(corev1.ServiceTypeNodePort))
				})

				It("will have the specified ports exposed when specified", func() {
					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1beta1.ClusterSpec{
							Expose: &v1beta1.ExposeConfig{
								NodePort: &v1beta1.NodePortConfig{
									ServerPort: ptr.To[int32](30010),
									ETCDPort:   ptr.To[int32](30011),
								},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					var service corev1.Service

					Eventually(func() corev1.ServiceType {
						serviceKey := client.ObjectKey{
							Name:      server.ServiceName(cluster.Name),
							Namespace: cluster.Namespace,
						}

						err := k8sClient.Get(ctx, serviceKey, &service)
						Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))

						return service.Spec.Type
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Equal(corev1.ServiceTypeNodePort))

					servicePorts := service.Spec.Ports
					Expect(servicePorts).NotTo(BeEmpty())
					Expect(servicePorts).To(HaveLen(2))

					serverPort := servicePorts[0]
					Expect(serverPort.Name).To(Equal("k3s-server-port"))
					Expect(serverPort.Port).To(BeEquivalentTo(443))
					Expect(serverPort.NodePort).To(BeEquivalentTo(30010))

					etcdPort := servicePorts[1]
					Expect(etcdPort.Name).To(Equal("k3s-etcd-port"))
					Expect(etcdPort.Port).To(BeEquivalentTo(2379))
					Expect(etcdPort.NodePort).To(BeEquivalentTo(30011))
				})

				It("will not expose the port when out of range", func() {
					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1beta1.ClusterSpec{
							Expose: &v1beta1.ExposeConfig{
								NodePort: &v1beta1.NodePortConfig{
									ETCDPort: ptr.To[int32](2222),
								},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					var service corev1.Service

					Eventually(func() corev1.ServiceType {
						serviceKey := client.ObjectKey{
							Name:      server.ServiceName(cluster.Name),
							Namespace: cluster.Namespace,
						}

						err := k8sClient.Get(ctx, serviceKey, &service)
						Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))

						return service.Spec.Type
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Equal(corev1.ServiceTypeNodePort))

					servicePorts := service.Spec.Ports
					Expect(servicePorts).NotTo(BeEmpty())
					Expect(servicePorts).To(HaveLen(1))

					serverPort := servicePorts[0]
					Expect(serverPort.Name).To(Equal("k3s-server-port"))
					Expect(serverPort.Port).To(BeEquivalentTo(443))
					Expect(serverPort.TargetPort.IntValue()).To(BeEquivalentTo(6443))
				})
			})

			When("exposing the cluster with loadbalancer", func() {
				It("will have a LoadBalancer service with the default ports exposed", func() {
					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1beta1.ClusterSpec{
							Expose: &v1beta1.ExposeConfig{
								LoadBalancer: &v1beta1.LoadBalancerConfig{},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					var service corev1.Service

					Eventually(func() error {
						serviceKey := client.ObjectKey{
							Name:      server.ServiceName(cluster.Name),
							Namespace: cluster.Namespace,
						}

						return k8sClient.Get(ctx, serviceKey, &service)
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Succeed())

					Expect(service.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))

					servicePorts := service.Spec.Ports
					Expect(servicePorts).NotTo(BeEmpty())
					Expect(servicePorts).To(HaveLen(2))

					serverPort := servicePorts[0]
					Expect(serverPort.Name).To(Equal("k3s-server-port"))
					Expect(serverPort.Port).To(BeEquivalentTo(443))
					Expect(serverPort.TargetPort.IntValue()).To(BeEquivalentTo(6443))

					etcdPort := servicePorts[1]
					Expect(etcdPort.Name).To(Equal("k3s-etcd-port"))
					Expect(etcdPort.Port).To(BeEquivalentTo(2379))
					Expect(etcdPort.TargetPort.IntValue()).To(BeEquivalentTo(2379))
				})
			})

			When("exposing the cluster with ingress", func() {
				It("will not be provisioned without a DNS name in the tlsSANs", func() {
					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1beta1.ClusterSpec{
							Expose: &v1beta1.ExposeConfig{
								Ingress: &v1beta1.IngressConfig{},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					// the Ingress hosts come from the tlsSANs: without one the generated
					// Ingress would have no rules and be rejected by the API server, so
					// the cluster should stay Pending with a validation error instead.
					Eventually(func(g Gomega) {
						err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)
						g.Expect(err).To(Not(HaveOccurred()))

						g.Expect(cluster.Status.Phase).To(Equal(v1beta1.ClusterPending))

						readyCondition := meta.FindStatusCondition(cluster.Status.Conditions, "Ready")
						g.Expect(readyCondition).To(Not(BeNil()))
						g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
						g.Expect(readyCondition.Reason).To(Equal("ValidationFailed"))
						g.Expect(readyCondition.Message).To(ContainSubstring("spec.tlsSANs"))
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Succeed())

					// no invalid Ingress should have been submitted
					ingressKey := client.ObjectKey{
						Name:      server.IngressName(cluster.Name),
						Namespace: cluster.Namespace,
					}

					var ingress networkingv1.Ingress
					Expect(apierrors.IsNotFound(k8sClient.Get(ctx, ingressKey, &ingress))).To(BeTrue())
				})

				It("will create an Ingress with only the DNS names from the tlsSANs", func() {
					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1beta1.ClusterSpec{
							// the IP is not a valid Ingress host and must be skipped
							TLSSANs: []string{"10.0.0.5", "my-cluster.example.com"},
							Expose: &v1beta1.ExposeConfig{
								Ingress: &v1beta1.IngressConfig{
									IngressClassName: "nginx",
									Annotations: map[string]string{
										"nginx.ingress.kubernetes.io/ssl-passthrough": "true",
									},
								},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					ingressKey := client.ObjectKey{
						Name:      server.IngressName(cluster.Name),
						Namespace: cluster.Namespace,
					}

					var ingress networkingv1.Ingress

					Eventually(func() error {
						return k8sClient.Get(ctx, ingressKey, &ingress)
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Succeed())

					Expect(ingress.Spec.IngressClassName).To(Equal(ptr.To("nginx")))
					Expect(ingress.Annotations).To(HaveKeyWithValue("nginx.ingress.kubernetes.io/ssl-passthrough", "true"))

					Expect(ingress.Spec.Rules).To(HaveLen(1))
					Expect(ingress.Spec.Rules[0].Host).To(Equal("my-cluster.example.com"))

					backend := ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service
					Expect(backend.Name).To(Equal(server.ServiceName(cluster.Name)))
					Expect(backend.Port.Number).To(BeEquivalentTo(443))
				})
			})

			When("exposing the cluster with nodePort and loadbalancer", func() {
				It("will fail", func() {
					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1beta1.ClusterSpec{
							Expose: &v1beta1.ExposeConfig{
								LoadBalancer: &v1beta1.LoadBalancerConfig{},
								NodePort:     &v1beta1.NodePortConfig{},
							},
						},
					}

					err := k8sClient.Create(ctx, cluster)
					Expect(err).To(HaveOccurred())
				})
			})
			When("adding addons to the cluster", func() {
				It("will create a statefulset with the correct addon volumes and volume mounts", func() {
					// Create the addon secret first
					addonSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-addon",
							Namespace: namespace,
						},
						Data: map[string][]byte{
							"manifest.yaml": []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-cm\n"),
						},
					}
					Expect(k8sClient.Create(ctx, addonSecret)).To(Succeed())

					// Create the cluster with an addon referencing the secret
					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1beta1.ClusterSpec{
							Addons: []v1beta1.Addon{
								{
									SecretRef: "test-addon",
								},
							},
						},
					}
					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					// Wait for the statefulset to be created and verify volumes/mounts
					var statefulSet appsv1.StatefulSet

					statefulSetName := k3kcontroller.SafeConcatNameWithPrefix(cluster.Name, "server")

					Eventually(func() error {
						return k8sClient.Get(ctx, client.ObjectKey{
							Name:      statefulSetName,
							Namespace: cluster.Namespace,
						}, &statefulSet)
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Succeed())

					// Verify the addon volume exists
					var addonVolume *corev1.Volume

					for i := range statefulSet.Spec.Template.Spec.Volumes {
						v := &statefulSet.Spec.Template.Spec.Volumes[i]
						if v.Name == "addon-test-addon" {
							addonVolume = v
							break
						}
					}

					Expect(addonVolume).NotTo(BeNil(), "addon volume should exist")
					Expect(addonVolume.VolumeSource.Secret).NotTo(BeNil())
					Expect(addonVolume.VolumeSource.Secret.SecretName).To(Equal("test-addon"))

					// Verify the addon volume mount exists in the first container
					containers := statefulSet.Spec.Template.Spec.Containers
					Expect(containers).NotTo(BeEmpty())

					var addonMount *corev1.VolumeMount

					for i := range containers[0].VolumeMounts {
						m := &containers[0].VolumeMounts[i]
						if m.Name == "addon-test-addon" {
							addonMount = m
							break
						}
					}

					Expect(addonMount).NotTo(BeNil(), "addon volume mount should exist")
					Expect(addonMount.MountPath).To(Equal("/var/lib/rancher/k3s/server/manifests/test-addon"))
					Expect(addonMount.ReadOnly).To(BeTrue())
				})

				It("will create volumes for multiple addons in the correct order", func() {
					// Create multiple addon secrets
					addonSecret1 := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "addon-one",
							Namespace: namespace,
						},
						Data: map[string][]byte{
							"manifest.yaml": []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm-one\n"),
						},
					}
					addonSecret2 := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "addon-two",
							Namespace: namespace,
						},
						Data: map[string][]byte{
							"manifest.yaml": []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm-two\n"),
						},
					}

					Expect(k8sClient.Create(ctx, addonSecret1)).To(Succeed())
					Expect(k8sClient.Create(ctx, addonSecret2)).To(Succeed())

					// Create the cluster with multiple addons in specific order
					cluster := &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1beta1.ClusterSpec{
							Addons: []v1beta1.Addon{
								{SecretRef: "addon-one"},
								{SecretRef: "addon-two"},
							},
						},
					}
					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					// Wait for the statefulset to be created
					var statefulSet appsv1.StatefulSet

					statefulSetName := k3kcontroller.SafeConcatNameWithPrefix(cluster.Name, "server")

					Eventually(func() error {
						return k8sClient.Get(ctx, client.ObjectKey{
							Name:      statefulSetName,
							Namespace: cluster.Namespace,
						}, &statefulSet)
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Succeed())

					// Verify both addon volumes exist and are in the correct order
					volumes := statefulSet.Spec.Template.Spec.Volumes

					// Extract only addon volumes (those starting with "addon-")
					var addonVolumes []corev1.Volume

					for _, v := range volumes {
						if strings.HasPrefix(v.Name, "addon-") {
							addonVolumes = append(addonVolumes, v)
						}
					}

					Expect(addonVolumes).To(HaveLen(2))
					Expect(addonVolumes[0].Name).To(Equal("addon-addon-one"))
					Expect(addonVolumes[1].Name).To(Equal("addon-addon-two"))

					// Verify both addon volume mounts exist and are in the correct order
					containers := statefulSet.Spec.Template.Spec.Containers
					Expect(containers).NotTo(BeEmpty())

					// Extract only addon mounts (those starting with "addon-")
					var addonMounts []corev1.VolumeMount

					for _, m := range containers[0].VolumeMounts {
						if strings.HasPrefix(m.Name, "addon-") {
							addonMounts = append(addonMounts, m)
						}
					}

					Expect(addonMounts).To(HaveLen(2))
					Expect(addonMounts[0].Name).To(Equal("addon-addon-one"))
					Expect(addonMounts[0].MountPath).To(Equal("/var/lib/rancher/k3s/server/manifests/addon-one"))
					Expect(addonMounts[1].Name).To(Equal("addon-addon-two"))
					Expect(addonMounts[1].MountPath).To(Equal("/var/lib/rancher/k3s/server/manifests/addon-two"))
				})
			})
		})
	})
})
