package k3k_test

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k3sv1 "github.com/k3s-io/api/k3s.cattle.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3ksnapshot "github.com/rancher/k3k/pkg/controller/snapshot"
	fwclient "github.com/rancher/k3k/tests/framework/client"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("Creating Etcd snapshots for shared mode cluster", Ordered, Label(snapshotTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeAll(func() {
		virtualCluster = NewVirtualClusterWithType(v1beta1.DynamicPersistenceMode)
		scheme := fwclient.NewScheme()
		err := k3sv1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())

		virtualCluster.CtrlClient = NewVirtualCtrlClient(virtualCluster.RestConfig, scheme)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})
	})

	When("Local Etcd snapshot object is created", func() {
		var snapshot *v1beta1.EtcdSnapshot

		BeforeAll(func() {
			ctx := GinkgoT().Context()

			snapshot = newSnapshot(virtualCluster.Cluster.Name, virtualCluster.Cluster.Namespace, "")

			err := k8sClient.Create(ctx, snapshot)
			Expect(err).ToNot(HaveOccurred())
		})

		It("local snapshot will be created locally and snapshot status updated", func() {
			ctx := GinkgoT().Context()

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(snapshot.Status.Filename).ToNot(BeEmpty())

				cond := meta.FindStatusCondition(snapshot.Status.Conditions, k3ksnapshot.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(k3ksnapshot.SuccessfulCreateSnapshotReason))
				g.Expect(cond.Message).To(ContainSubstring(`Snapshot was created`))
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})

	When("S3 Etcd snapshot object is created", func() {
		var (
			snapshot *v1beta1.EtcdSnapshot
			endpoint string
		)

		BeforeAll(func() {
			ctx := GinkgoT().Context()
			namespace := virtualCluster.Cluster.Namespace

			deployS3MockInCluster(namespace)

			endpoint = fmt.Sprintf("s3-mock.%s.svc:%d", namespace, s3MockPort)

			secret := newS3ConfigSecret(s3ConfigSecretName, namespace, endpoint)
			err := k8sClient.Create(ctx, secret)
			Expect(err).ToNot(HaveOccurred())

			snapshot = newSnapshot(virtualCluster.Cluster.Name, namespace, s3ConfigSecretName)

			err = k8sClient.Create(ctx, snapshot)
			Expect(err).ToNot(HaveOccurred())
		})

		It("S3 snapshot will be created and snapshot status updated", func() {
			ctx := GinkgoT().Context()

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(snapshot.Status.Filename).ToNot(BeEmpty())

				cond := meta.FindStatusCondition(snapshot.Status.Conditions, k3ksnapshot.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(k3ksnapshot.SuccessfulCreateSnapshotReason))
				g.Expect(cond.Message).To(ContainSubstring(`Snapshot was created`))
			}).
				WithTimeout(time.Minute * 3).
				WithPolling(time.Second * 2).
				Should(Succeed())
		})

		It("S3 snapshot will be uploaded to the S3 bucket", func() {
			ctx := GinkgoT().Context()

			Eventually(func(g Gomega) {
				var snapshotFileList k3sv1.ETCDSnapshotFileList

				err := virtualCluster.CtrlClient.List(ctx, &snapshotFileList)
				g.Expect(err).ToNot(HaveOccurred())

				var snapshotFile *k3sv1.ETCDSnapshotFile

				for i := range snapshotFileList.Items {
					file := snapshotFileList.Items[i]
					if file.Spec.SnapshotName == snapshot.Status.Filename && file.Spec.S3 != nil {
						snapshotFile = &file
						break
					}
				}

				g.Expect(snapshotFile).NotTo(BeNil())
				g.Expect(snapshotFile.Spec.Location).To(HavePrefix("s3://"))
				g.Expect(snapshotFile.Spec.Location).To(ContainSubstring(s3MockBucket))
				g.Expect(snapshotFile.Spec.S3.Endpoint).To(Equal(endpoint))
				g.Expect(snapshotFile.Spec.S3.Bucket).To(Equal(s3MockBucket))
				g.Expect(snapshotFile.Spec.S3.Prefix).To(ContainSubstring(s3MockFolder))
				g.Expect(snapshotFile.Status.Size).NotTo(BeNil())
				g.Expect(snapshotFile.Status.Size.IsZero()).To(BeFalse())
			}).
				WithTimeout(time.Minute * 2).
				WithPolling(time.Second * 2).
				Should(Succeed())
		})

		It("S3 snapshot will be removed from the S3 bucket when deleted", func() {
			ctx := GinkgoT().Context()

			filename := snapshot.Status.Filename
			Expect(filename).ToNot(BeEmpty())

			err := k8sClient.Delete(ctx, snapshot)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				g.Expect(err).To(HaveOccurred())
				g.Expect(client.IgnoreNotFound(err)).ToNot(HaveOccurred())
			}).
				WithTimeout(time.Minute * 2).
				WithPolling(time.Second * 2).
				Should(Succeed())

			Eventually(func(g Gomega) {
				var snapshotFileList k3sv1.ETCDSnapshotFileList

				err := virtualCluster.CtrlClient.List(ctx, &snapshotFileList)
				g.Expect(err).ToNot(HaveOccurred())

				for _, file := range snapshotFileList.Items {
					if strings.HasPrefix(file.Spec.Location, "s3://") {
						g.Expect(file.Spec.SnapshotName).ToNot(Equal(filename))
					}
				}
			}).
				WithTimeout(time.Minute * 2).
				WithPolling(time.Second * 2).
				Should(Succeed())
		})
	})
})

var _ = When("Creating Etcd snapshots for virtual mode cluster", Ordered, Label(snapshotTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeAll(func() {
		virtualCluster = NewVirtualClusterWithOpts(func(c *v1beta1.Cluster) {
			c.Spec.Mode = v1beta1.VirtualClusterMode
			c.Spec.Persistence.Type = v1beta1.DynamicPersistenceMode
		})

		scheme := fwclient.NewScheme()
		err := k3sv1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())

		virtualCluster.CtrlClient = NewVirtualCtrlClient(virtualCluster.RestConfig, scheme)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})
	})

	When("Local Etcd snapshot object is created", func() {
		var snapshot *v1beta1.EtcdSnapshot

		BeforeAll(func() {
			ctx := GinkgoT().Context()

			snapshot = newSnapshot(virtualCluster.Cluster.Name, virtualCluster.Cluster.Namespace, "")

			err := k8sClient.Create(ctx, snapshot)
			Expect(err).ToNot(HaveOccurred())
		})

		It("local snapshot will be created locally and snapshot status updated", func() {
			ctx := GinkgoT().Context()

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(snapshot.Status.Filename).ToNot(BeEmpty())

				cond := meta.FindStatusCondition(snapshot.Status.Conditions, k3ksnapshot.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(k3ksnapshot.SuccessfulCreateSnapshotReason))
				g.Expect(cond.Message).To(ContainSubstring(`Snapshot was created`))
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})

	When("S3 Etcd snapshot object is created", func() {
		var (
			snapshot *v1beta1.EtcdSnapshot
			endpoint string
		)

		BeforeAll(func() {
			ctx := GinkgoT().Context()
			namespace := virtualCluster.Cluster.Namespace

			deployS3MockInCluster(namespace)

			endpoint = fmt.Sprintf("s3-mock.%s.svc:%d", namespace, s3MockPort)

			secret := newS3ConfigSecret(s3ConfigSecretName, namespace, endpoint)
			err := k8sClient.Create(ctx, secret)
			Expect(err).ToNot(HaveOccurred())

			snapshot = newSnapshot(virtualCluster.Cluster.Name, namespace, s3ConfigSecretName)

			err = k8sClient.Create(ctx, snapshot)
			Expect(err).ToNot(HaveOccurred())
		})

		It("S3 snapshot will be created and snapshot status updated", func() {
			ctx := GinkgoT().Context()

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(snapshot.Status.Filename).ToNot(BeEmpty())

				cond := meta.FindStatusCondition(snapshot.Status.Conditions, k3ksnapshot.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(k3ksnapshot.SuccessfulCreateSnapshotReason))
				g.Expect(cond.Message).To(ContainSubstring(`Snapshot was created`))
			}).
				WithTimeout(time.Minute * 3).
				WithPolling(time.Second * 2).
				Should(Succeed())
		})

		It("S3 snapshot will be uploaded to the S3 bucket", func() {
			ctx := GinkgoT().Context()

			Eventually(func(g Gomega) {
				var snapshotFileList k3sv1.ETCDSnapshotFileList

				err := virtualCluster.CtrlClient.List(ctx, &snapshotFileList)
				g.Expect(err).ToNot(HaveOccurred())

				var snapshotFile *k3sv1.ETCDSnapshotFile

				for i := range snapshotFileList.Items {
					file := snapshotFileList.Items[i]
					if file.Spec.SnapshotName == snapshot.Status.Filename && file.Spec.S3 != nil {
						snapshotFile = &file
						break
					}
				}

				g.Expect(snapshotFile).NotTo(BeNil())
				g.Expect(snapshotFile.Spec.Location).To(HavePrefix("s3://"))
				g.Expect(snapshotFile.Spec.Location).To(ContainSubstring(s3MockBucket))
				g.Expect(snapshotFile.Spec.S3.Endpoint).To(Equal(endpoint))
				g.Expect(snapshotFile.Spec.S3.Bucket).To(Equal(s3MockBucket))
				g.Expect(snapshotFile.Spec.S3.Prefix).To(ContainSubstring(s3MockFolder))
				g.Expect(snapshotFile.Status.Size).NotTo(BeNil())
				g.Expect(snapshotFile.Status.Size.IsZero()).To(BeFalse())
			}).
				WithTimeout(time.Minute * 2).
				WithPolling(time.Second * 2).
				Should(Succeed())
		})

		It("S3 snapshot will be removed from the S3 bucket when deleted", func() {
			ctx := GinkgoT().Context()

			filename := snapshot.Status.Filename
			Expect(filename).ToNot(BeEmpty())

			err := k8sClient.Delete(ctx, snapshot)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				g.Expect(err).To(HaveOccurred())
				g.Expect(client.IgnoreNotFound(err)).ToNot(HaveOccurred())
			}).
				WithTimeout(time.Minute * 2).
				WithPolling(time.Second * 2).
				Should(Succeed())

			Eventually(func(g Gomega) {
				var snapshotFileList k3sv1.ETCDSnapshotFileList

				err := virtualCluster.CtrlClient.List(ctx, &snapshotFileList)
				g.Expect(err).ToNot(HaveOccurred())

				for _, file := range snapshotFileList.Items {
					if strings.HasPrefix(file.Spec.Location, "s3://") {
						g.Expect(file.Spec.SnapshotName).ToNot(Equal(filename))
					}
				}
			}).
				WithTimeout(time.Minute * 2).
				WithPolling(time.Second * 2).
				Should(Succeed())
		})
	})
})
