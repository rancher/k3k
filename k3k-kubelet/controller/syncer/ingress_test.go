package syncer_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/controller/syncer"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var IngressTests = func() {
	var (
		namespace string
		cluster   v1alpha1.Cluster
	)

	BeforeEach(func() {
		ctx := context.Background()

		ns := v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"},
		}
		err := hostTestEnv.k8sClient.Create(ctx, &ns)
		Expect(err).NotTo(HaveOccurred())

		namespace = ns.Name

		cluster = v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cluster-",
				Namespace:    namespace,
			},
		}
		err = hostTestEnv.k8sClient.Create(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		err = syncer.AddIngressSyncer(ctx, virtManager, hostManager, cluster.Name, cluster.Namespace)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		ns := v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := hostTestEnv.k8sClient.Delete(context.Background(), &ns)
		Expect(err).NotTo(HaveOccurred())
	})

	It("creates a Ingress on the host cluster", func() {
		ctx := context.Background()

		ingress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "ingress-",
				Namespace:    "default",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{
					{
						Host: "test.com",
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{
										Path:     "/",
										PathType: ptr.To(networkingv1.PathTypePrefix),
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: "test-service",
												Port: networkingv1.ServiceBackendPort{
													Name: "test-port",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, ingress)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created Ingress %s in virtual cluster", ingress.Name))

		var hostIngress networkingv1.Ingress
		hostIngressName := translateName(cluster, ingress.Namespace, ingress.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostIngressName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostIngress)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created Ingress %s in host cluster", hostIngressName))

		Expect(len(hostIngress.Spec.Rules)).To(Equal(1))
		Expect(hostIngress.Spec.Rules[0].Host).To(Equal("test.com"))
		Expect(hostIngress.Spec.Rules[0].HTTP.Paths[0].Path).To(Equal("/"))
		Expect(hostIngress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name).To(Equal(translateName(cluster, ingress.Namespace, "test-service")))
		Expect(hostIngress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Name).To(Equal("test-port"))

		GinkgoWriter.Printf("labels: %v\n", hostIngress.Labels)
	})

	It("updates a Ingress on the host cluster", func() {
		ctx := context.Background()

		ingress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "ingress-",
				Namespace:    "default",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{
					{
						Host: "test.com",
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{
										Path:     "/",
										PathType: ptr.To(networkingv1.PathTypePrefix),
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: "test-service",
												Port: networkingv1.ServiceBackendPort{
													Name: "test-port",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, ingress)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created Ingress %s in virtual cluster", ingress.Name))

		var hostIngress networkingv1.Ingress
		hostIngressName := translateName(cluster, ingress.Namespace, ingress.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostIngressName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostIngress)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created Ingress %s in host cluster", hostIngressName))

		Expect(len(hostIngress.Spec.Rules)).To(Equal(1))
		Expect(hostIngress.Spec.Rules[0].Host).To(Equal("test.com"))
		Expect(hostIngress.Spec.Rules[0].HTTP.Paths[0].Path).To(Equal("/"))
		Expect(hostIngress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name).To(Equal(translateName(cluster, ingress.Namespace, "test-service")))
		Expect(hostIngress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Name).To(Equal("test-port"))

		key := client.ObjectKeyFromObject(ingress)
		err = virtTestEnv.k8sClient.Get(ctx, key, ingress)
		Expect(err).NotTo(HaveOccurred())

		ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "test-service-updated"

		// update virtual ingress
		err = virtTestEnv.k8sClient.Update(ctx, ingress)
		Expect(err).NotTo(HaveOccurred())

		// check hostIngress
		Eventually(func() string {
			key := client.ObjectKey{Name: hostIngressName, Namespace: namespace}
			err = hostTestEnv.k8sClient.Get(ctx, key, &hostIngress)
			Expect(err).NotTo(HaveOccurred())
			return hostIngress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(Equal(translateName(cluster, ingress.Namespace, "test-service-updated")))
	})

	It("deletes a Ingress on the host cluster", func() {
		ctx := context.Background()

		ingress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "ingress-",
				Namespace:    "default",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{
					{
						Host: "test.com",
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{
										Path:     "/",
										PathType: ptr.To(networkingv1.PathTypePrefix),
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: "test-service",
												Port: networkingv1.ServiceBackendPort{
													Name: "test-port",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, ingress)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created Ingress %s in virtual cluster", ingress.Name))

		var hostIngress networkingv1.Ingress
		hostIngressName := translateName(cluster, ingress.Namespace, ingress.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostIngressName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostIngress)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created Ingress %s in host cluster", hostIngressName))

		Expect(len(hostIngress.Spec.Rules)).To(Equal(1))
		Expect(hostIngress.Spec.Rules[0].Host).To(Equal("test.com"))
		Expect(hostIngress.Spec.Rules[0].HTTP.Paths[0].Path).To(Equal("/"))
		Expect(hostIngress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name).To(Equal(translateName(cluster, ingress.Namespace, "test-service")))
		Expect(hostIngress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Name).To(Equal("test-port"))

		err = virtTestEnv.k8sClient.Delete(ctx, ingress)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			key := client.ObjectKey{Name: hostIngressName, Namespace: namespace}
			err := hostTestEnv.k8sClient.Get(ctx, key, &hostIngress)
			return apierrors.IsNotFound(err)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeTrue())
	})
}
