package cluster_test

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cluster Controller", Label("controller"), Label("Cluster"), func() {

	Context("creating a Cluster", func() {

		var (
			namespace string
		)

		BeforeEach(func() {
			createdNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"}}
			err := k8sClient.Create(context.Background(), createdNS)
			Expect(err).To(Not(HaveOccurred()))
			namespace = createdNS.Name
		})

		When("creating a Cluster", func() {

			var cluster *v1alpha1.Cluster

			BeforeEach(func() {
				cluster = &v1alpha1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))
			})

			It("will be created with some defaults", func() {
				Expect(cluster.Spec.Mode).To(Equal(v1alpha1.SharedClusterMode))
				Expect(cluster.Spec.Agents).To(Equal(ptr.To[int32](0)))
				Expect(cluster.Spec.Servers).To(Equal(ptr.To[int32](1)))
				Expect(cluster.Spec.Version).To(BeEmpty())
				// TOFIX
				// Expect(cluster.Spec.Persistence.Type).To(Equal(v1alpha1.DynamicPersistenceMode))

				serverVersion, err := k8s.DiscoveryClient.ServerVersion()
				Expect(err).To(Not(HaveOccurred()))
				expectedHostVersion := fmt.Sprintf("%s-k3s1", serverVersion.GitVersion)

				Eventually(func() string {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)
					Expect(err).To(Not(HaveOccurred()))
					return cluster.Status.HostVersion

				}).
					WithTimeout(time.Second * 30).
					WithPolling(time.Second).
					Should(Equal(expectedHostVersion))

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
			})

			When("exposing the cluster with nodePort and custom ports", func() {
				It("will have a NodePort service with the specified port exposed", func() {
					cluster.Spec.Expose = &v1alpha1.ExposeConfig{
						NodePort: &v1alpha1.NodePortConfig{
							ServerPort:  ptr.To[int32](30010),
							ServicePort: ptr.To[int32](30011),
							ETCDPort:    ptr.To[int32](30012),
						},
					}

					err := k8sClient.Update(ctx, cluster)
					Expect(err).To(Not(HaveOccurred()))

					var service v1.Service

					Eventually(func() v1.ServiceType {
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
						Should(Equal(v1.ServiceTypeNodePort))

					servicePorts := service.Spec.Ports
					Expect(servicePorts).NotTo(BeEmpty())
					Expect(servicePorts).To(HaveLen(3))

					Expect(servicePorts).To(ContainElement(
						And(
							HaveField("Name", "k3s-server-port"),
							HaveField("Port", BeEquivalentTo(6443)),
							HaveField("NodePort", BeEquivalentTo(30010)),
						),
					))
					Expect(servicePorts).To(ContainElement(
						And(
							HaveField("Name", "k3s-service-port"),
							HaveField("Port", BeEquivalentTo(443)),
							HaveField("NodePort", BeEquivalentTo(30011)),
						),
					))
					Expect(servicePorts).To(ContainElement(
						And(
							HaveField("Name", "k3s-etcd-port"),
							HaveField("Port", BeEquivalentTo(2379)),
							HaveField("NodePort", BeEquivalentTo(30012)),
						),
					))
				})
			})
		})
	})
})
