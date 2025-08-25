package syncer_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/controller/syncer"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var PVCTests = func() {
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
					PersistentVolumeClaims: v1alpha1.PersistentVolumeClaimSyncConfig{
						Enabled: true,
					},
				},
			},
		}
		err = hostTestEnv.k8sClient.Create(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		err = syncer.AddPVCSyncer(ctx, virtManager, hostManager, cluster.Name, cluster.Namespace)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		ns := v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := hostTestEnv.k8sClient.Delete(context.Background(), &ns)
		Expect(err).NotTo(HaveOccurred())
	})

	It("creates a pvc on the host cluster", func() {
		ctx := context.Background()

		pvc := &v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "pvc-",
				Namespace:    "default",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Spec: v1.PersistentVolumeClaimSpec{
				StorageClassName: ptr.To("test-sc"),
				AccessModes: []v1.PersistentVolumeAccessMode{
					v1.ReadOnlyMany,
				},
				Resources: v1.VolumeResourceRequirements{
					Requests: v1.ResourceList{
						"storage": resource.MustParse("1G"),
					},
				},
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, pvc)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created PVC %s in virtual cluster", pvc.Name))

		var hostPVC v1.PersistentVolumeClaim
		hostPVCName := translateName(cluster, pvc.Namespace, pvc.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostPVCName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostPVC)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created PVC %s in host cluster", hostPVCName))

		Expect(*hostPVC.Spec.StorageClassName).To(Equal("test-sc"))

		GinkgoWriter.Printf("labels: %v\n", hostPVC.Labels)
	})
}
