package syncer_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/controller/syncer"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var HostServiceTests = func() {
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
			Spec: v1alpha1.ClusterSpec{
				Sync: v1alpha1.SyncConfig{
					Services: v1alpha1.ServiceSyncConfig{
						Enabled: true,
					},
				},
			},
		}
		err = hostTestEnv.k8sClient.Create(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		err = syncer.AddServiceSyncer(ctx, virtManager, hostManager, cluster.Name, cluster.Namespace)
		Expect(err).NotTo(HaveOccurred())

		err = syncer.AddHostServiceStatusSyncer(ctx, virtManager, hostManager, cluster.Name, cluster.Namespace)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		ns := v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := hostTestEnv.k8sClient.Delete(context.Background(), &ns)
		Expect(err).NotTo(HaveOccurred())
	})

	It("sync service lb status back to virtual cluster", func() {
		ctx := context.Background()

		service := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "service-",
				Namespace:    "default",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Spec: v1.ServiceSpec{
				Type: v1.ServiceTypeLoadBalancer,
				Ports: []v1.ServicePort{
					{
						Name:       "test-port",
						Port:       8888,
						TargetPort: intstr.FromInt32(8888),
					},
				},
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, service)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created service %s in virtual cluster", service.Name))

		var hostService v1.Service
		hostServiceName := translateName(cluster, service.Namespace, service.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostServiceName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostService)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created Service %s in host cluster", hostServiceName))

		Expect(hostService.Spec.Type).To(Equal(v1.ServiceTypeLoadBalancer))
		Expect(hostService.Spec.Ports[0].Name).To(Equal("test-port"))
		Expect(hostService.Spec.Ports[0].Port).To(Equal(int32(8888)))

		// Patch the host service with status LB IP
		hostService.Status = v1.ServiceStatus{
			LoadBalancer: v1.LoadBalancerStatus{
				Ingress: []v1.LoadBalancerIngress{
					{
						IP: "10.10.10.10",
					},
				},
			},
		}
		err = hostTestEnv.k8sClient.Status().Update(ctx, &hostService)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() string {
			var virtService v1.Service
			err = virtTestEnv.k8sClient.Get(ctx, client.ObjectKeyFromObject(service), &virtService)
			Expect(err).NotTo(HaveOccurred())
			if virtService.Status.LoadBalancer.Ingress != nil {
				return virtService.Status.LoadBalancer.Ingress[0].IP
			}
			return ""
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(Equal("10.10.10.10"))

		GinkgoWriter.Printf("labels: %v\n", hostService.Labels)
	})
}
