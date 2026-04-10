package k3k_test

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/controller/cluster"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a shared mode cluster is created", Ordered, Label(e2eTestLabel), func() {
	var (
		ctx            context.Context
		virtualCluster *VirtualCluster
	)

	BeforeAll(func() {
		ctx = context.Background()
		virtualCluster = NewVirtualCluster()

		storageClassEnabled := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
				Labels: map[string]string{
					cluster.SyncEnabledLabelKey: "true",
				},
			},
			Provisioner: "my-provisioner",
		}

		storageClassEnabled, err := k8s.StorageV1().StorageClasses().Create(ctx, storageClassEnabled, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		storageClassDisabled := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
				Labels: map[string]string{
					cluster.SyncEnabledLabelKey: "false",
				},
			},
			Provisioner: "my-provisioner",
		}

		storageClassDisabled, err = k8s.StorageV1().StorageClasses().Create(ctx, storageClassDisabled, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)

			err = k8s.StorageV1().StorageClasses().Delete(ctx, storageClassEnabled.Name, metav1.DeleteOptions{})
			Expect(err).To(Not(HaveOccurred()))

			err = k8s.StorageV1().StorageClasses().Delete(ctx, storageClassDisabled.Name, metav1.DeleteOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})
	})

	It("has disabled the storage classes sync", func() {
		Expect(virtualCluster.Cluster.Spec.Sync).To(Not(BeNil()))
		Expect(virtualCluster.Cluster.Spec.Sync.StorageClasses.Enabled).To(BeFalse())
	})

	It("doesn't have storage classes", func() {
		virtualStorageClasses, err := virtualCluster.Client.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))
		Expect(virtualStorageClasses.Items).To(HaveLen(0))
	})

	It("has some storage classes in the host", func() {
		hostStorageClasses, err := k8s.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))
		Expect(hostStorageClasses.Items).To(Not(HaveLen(0)))
	})

	It("can create storage classes in the virtual cluster", func() {
		storageClass := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
			Provisioner: "my-provisioner",
		}

		storageClass, err := virtualCluster.Client.StorageV1().StorageClasses().Create(ctx, storageClass, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		virtualStorageClasses, err := virtualCluster.Client.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))
		Expect(virtualStorageClasses.Items).To(HaveLen(1))
		Expect(virtualStorageClasses.Items[0].Name).To(Equal(storageClass.Name))
	})

	When("enabling the storage class sync", Ordered, func() {
		BeforeAll(func() {
			GinkgoWriter.Println("Enabling the storage class sync")

			original := virtualCluster.Cluster.DeepCopy()

			virtualCluster.Cluster.Spec.Sync.StorageClasses.Enabled = true

			err := k8sClient.Patch(ctx, virtualCluster.Cluster, client.MergeFrom(original))
			Expect(err).To(Not(HaveOccurred()))

			Eventually(func(g Gomega) {
				key := client.ObjectKeyFromObject(virtualCluster.Cluster)
				g.Expect(k8sClient.Get(ctx, key, virtualCluster.Cluster)).To(Succeed())
				g.Expect(virtualCluster.Cluster.Spec.Sync.StorageClasses.Enabled).To(BeTrue())
			}).
				WithTimeout(time.Second * 10).
				WithPolling(time.Second).
				Should(Succeed())
		})

		It("will sync host storage classes with the sync enabled", func() {
			Eventually(func(g Gomega) {
				hostStorageClasses, err := k8s.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
				Expect(err).To(Not(HaveOccurred()))

				for _, hostSC := range hostStorageClasses.Items {
					_, err := virtualCluster.Client.StorageV1().StorageClasses().Get(ctx, hostSC.Name, metav1.GetOptions{})

					if syncEnabled, found := hostSC.Labels[cluster.SyncEnabledLabelKey]; !found || syncEnabled == "true" {
						g.Expect(err).To(Not(HaveOccurred()))
					}
				}
			}).
				MustPassRepeatedly(5).
				WithPolling(time.Second).
				WithTimeout(time.Second * 30).
				Should(Succeed())
		})

		It("will not sync host storage classes with the sync disabled", func() {
			Eventually(func(g Gomega) {
				hostStorageClasses, err := k8s.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
				Expect(err).To(Not(HaveOccurred()))

				for _, hostSC := range hostStorageClasses.Items {
					_, err := virtualCluster.Client.StorageV1().StorageClasses().Get(ctx, hostSC.Name, metav1.GetOptions{})

					if hostSC.Labels[cluster.SyncEnabledLabelKey] == "false" {
						g.Expect(err).To(HaveOccurred())
						g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
					}
				}
			}).
				MustPassRepeatedly(5).
				WithPolling(time.Second).
				WithTimeout(time.Second * 30).
				Should(Succeed())
		})
	})

	When("editing a synced storage class in the host cluster", Ordered, func() {
		var syncedStorageClass *storagev1.StorageClass

		BeforeAll(func() {
			hostStorageClasses, err := k8s.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
			Expect(err).To(Not(HaveOccurred()))

			for _, hostSC := range hostStorageClasses.Items {
				if syncEnabled, found := hostSC.Labels[cluster.SyncEnabledLabelKey]; !found || syncEnabled == "true" {
					syncedStorageClass = &hostSC
					break
				}
			}

			Expect(syncedStorageClass).To(Not(BeNil()))

			syncedStorageClass.Labels["foo"] = "bar"
			_, err = k8s.StorageV1().StorageClasses().Update(ctx, syncedStorageClass, metav1.UpdateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("will update the synced storage class in the virtual cluster", func() {
			Eventually(func(g Gomega) {
				_, err := virtualCluster.Client.StorageV1().StorageClasses().Get(ctx, syncedStorageClass.Name, metav1.GetOptions{})
				g.Expect(err).To(Not(HaveOccurred()))
				g.Expect(syncedStorageClass.Labels).Should(HaveKeyWithValue("foo", "bar"))
			}).
				MustPassRepeatedly(5).
				WithPolling(time.Second).
				WithTimeout(time.Second * 30).
				Should(Succeed())
		})
	})
})
