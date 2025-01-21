package cluster_test

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cluster Controller", func() {

	Context("creating a Cluster", func() {

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

			It("should have been created with some defaults", func() {
				cluster := &v1alpha1.Cluster{
					ObjectMeta: v1.ObjectMeta{
						GenerateName: "clusterset-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				Expect(cluster.Spec.Mode).To(Equal(v1alpha1.SharedClusterMode))
				Expect(cluster.Spec.Agents).To(Equal(ptr.To[int32](0)))
				Expect(cluster.Spec.Servers).To(Equal(ptr.To[int32](1)))
				Expect(cluster.Spec.Version).To(BeEmpty())

				serverVersion, err := k8s.DiscoveryClient.ServerVersion()
				Expect(err).To(Not(HaveOccurred()))
				expectedHostVersion := fmt.Sprintf("v%s.%s.0-k3s1", serverVersion.Major, serverVersion.Minor)

				Eventually(func() string {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)
					Expect(err).To(Not(HaveOccurred()))
					return cluster.Status.HostVersion

				}).
					WithTimeout(time.Second * 30).
					WithPolling(time.Second).
					Should(Equal(expectedHostVersion))
			})
		})
	})
})
