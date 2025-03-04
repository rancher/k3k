package k3k_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = When("two virtual clusters are installed", Label("e2e"), func() {

	var (
		cluster1 *v1alpha1.Cluster
		//cluster2 v1alpha1.Cluster
	)

	BeforeEach(func() {
		createVirtualCluster := func() *v1alpha1.Cluster {
			namespace := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{GenerateName: "ns-"}}

			namespace, err := k8s.CoreV1().Namespaces().Create(context.Background(), namespace, v1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			cluster := &v1alpha1.Cluster{
				ObjectMeta: v1.ObjectMeta{
					GenerateName: "cluster-",
					Namespace:    namespace.Name,
				},
				Spec: v1alpha1.ClusterSpec{
					TLSSANs: []string{hostIP},
					Expose: &v1alpha1.ExposeConfig{
						NodePort: &v1alpha1.NodePortConfig{},
					},
					Persistence: v1alpha1.PersistenceConfig{
						Type: v1alpha1.EphemeralPersistenceMode,
					},
				},
			}

			NewVirtualCluster(cluster)

			return cluster
		}

		cluster1 = createVirtualCluster()
		//cluster2 = createVirtualCluster()
	})

	It("can create two nginx pods in each of them", func() {
		createNginxPod := func(cluster *v1alpha1.Cluster) {
			ctx := context.Background()

			By("Waiting to get a kubernetes client for the virtual cluster1")
			virtualK8sClient1 := NewVirtualK8sClient(cluster)

			nginxPod := &corev1.Pod{
				ObjectMeta: v1.ObjectMeta{
					Name:      "nginx",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx",
					}},
				},
			}

			nginxPod, err := virtualK8sClient1.CoreV1().Pods(nginxPod.Namespace).Create(ctx, nginxPod, v1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			// check that the nginx Pod is up and running in the host cluster
			Eventually(func() bool {
				//labelSelector := fmt.Sprintf("%s=%s", translate.ClusterNameLabel, cluster.Namespace)
				podList, err := k8s.CoreV1().Pods(cluster.Namespace).List(ctx, v1.ListOptions{})
				Expect(err).To(Not(HaveOccurred()))

				for _, pod := range podList.Items {
					resourceName := pod.Annotations[translate.ResourceNameAnnotation]
					resourceNamespace := pod.Annotations[translate.ResourceNamespaceAnnotation]

					if resourceName == nginxPod.Name && resourceNamespace == nginxPod.Namespace {
						fmt.Fprintf(GinkgoWriter,
							"pod=%s resource=%s/%s status=%s\n",
							pod.Name, resourceNamespace, resourceName, pod.Status.Phase,
						)

						return pod.Status.Phase == corev1.PodRunning
					}
				}

				return false
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second * 5).
				Should(BeTrue())
		}

		createNginxPod(cluster1)
	})
})
