package k3k_test

import (
	"context"
	"time"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/policy"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a shared mode cluster is created in a namespace with a policy", Ordered, Label(e2eTestLabel), func() {
	var (
		ctx            context.Context
		virtualCluster *VirtualCluster
		vcp            *v1beta1.VirtualClusterPolicy
	)

	BeforeAll(func() {
		ctx = context.Background()

		// 1. Create StorageClasses in host
		storageClassEnabled := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-policy-enabled-",
				Labels: map[string]string{
					cluster.SyncEnabledLabelKey: "true",
				},
			},
			Provisioner: "my-provisioner",
		}

		var err error

		storageClassEnabled, err = k8s.StorageV1().StorageClasses().Create(ctx, storageClassEnabled, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		storageClassDisabled := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-policy-disabled-",
				Labels: map[string]string{
					cluster.SyncEnabledLabelKey: "false",
				},
			},
			Provisioner: "my-provisioner",
		}

		storageClassDisabled, err = k8s.StorageV1().StorageClasses().Create(ctx, storageClassDisabled, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// 2. Create VirtualClusterPolicy with StorageClass sync enabled
		vcp = &v1beta1.VirtualClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "vcp-sync-sc-",
			},
			Spec: v1beta1.VirtualClusterPolicySpec{
				Sync: &v1beta1.SyncConfig{
					StorageClasses: v1beta1.StorageClassSyncConfig{
						Enabled: true,
					},
				},
			},
		}
		err = k8sClient.Create(ctx, vcp)
		Expect(err).To(Not(HaveOccurred()))

		// 3. Create Namespace with policy label
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "ns-vcp-",
				Labels: map[string]string{
					policy.PolicyNameLabelKey: vcp.Name,
				},
			},
		}
		// We use the k8s clientset for namespace creation to stay consistent with other tests
		ns, err = k8s.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// 4. Create VirtualCluster in that namespace
		// The cluster doesn't have storage class sync enabled in its spec
		clusterObj := NewCluster(ns.Name)
		clusterObj.Spec.Sync = &v1beta1.SyncConfig{
			StorageClasses: v1beta1.StorageClassSyncConfig{
				Enabled: false,
			},
		}
		clusterObj.Spec.Expose.NodePort.ServerPort = ptr.To[int32](30000)

		CreateCluster(clusterObj)

		client, restConfig, kubeconfig := NewVirtualK8sClientAndKubeconfig(clusterObj)
		virtualCluster = &VirtualCluster{
			Cluster:    clusterObj,
			RestConfig: restConfig,
			Client:     client,
			Kubeconfig: kubeconfig,
		}

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, ns.Name)

			err = k8s.StorageV1().StorageClasses().Delete(ctx, storageClassEnabled.Name, metav1.DeleteOptions{})
			Expect(err).To(Not(HaveOccurred()))

			err = k8s.StorageV1().StorageClasses().Delete(ctx, storageClassDisabled.Name, metav1.DeleteOptions{})
			Expect(err).To(Not(HaveOccurred()))

			err = k8sClient.Delete(ctx, vcp)
			Expect(err).To(Not(HaveOccurred()))
		})
	})

	It("has the storage classes sync enabled from the policy", func() {
		Eventually(func(g Gomega) {
			key := client.ObjectKeyFromObject(virtualCluster.Cluster)
			g.Expect(k8sClient.Get(ctx, key, virtualCluster.Cluster)).To(Succeed())
			g.Expect(virtualCluster.Cluster.Status.Policy).To(Not(BeNil()))
			g.Expect(virtualCluster.Cluster.Status.Policy.Sync).To(Not(BeNil()))
			g.Expect(virtualCluster.Cluster.Status.Policy.Sync.StorageClasses.Enabled).To(BeTrue())
		}).
			WithTimeout(time.Second * 30).
			WithPolling(time.Second).
			Should(Succeed())
	})

	It("will sync host storage classes with the sync enabled in the host", func() {
		Eventually(func(g Gomega) {
			hostStorageClasses, err := k8s.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
			g.Expect(err).To(Not(HaveOccurred()))

			for _, hostSC := range hostStorageClasses.Items {
				// We only care about the storage classes we created for this test to avoid noise
				if hostSC.Labels[cluster.SyncEnabledLabelKey] == "true" {
					_, err := virtualCluster.Client.StorageV1().StorageClasses().Get(ctx, hostSC.Name, metav1.GetOptions{})
					g.Expect(err).To(Not(HaveOccurred()))
				}
			}
		}).
			WithPolling(time.Second).
			WithTimeout(time.Second * 60).
			Should(Succeed())
	})

	It("will not sync host storage classes with the sync disabled in the host", func() {
		Eventually(func(g Gomega) {
			hostStorageClasses, err := k8s.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
			g.Expect(err).To(Not(HaveOccurred()))

			for _, hostSC := range hostStorageClasses.Items {
				if hostSC.Labels[cluster.SyncEnabledLabelKey] == "false" {
					_, err := virtualCluster.Client.StorageV1().StorageClasses().Get(ctx, hostSC.Name, metav1.GetOptions{})
					g.Expect(err).To(HaveOccurred())
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				}
			}
		}).
			WithPolling(time.Second).
			WithTimeout(time.Second * 60).
			Should(Succeed())
	})

	When("disabling the storage class sync in the policy", Ordered, func() {
		BeforeAll(func() {
			original := vcp.DeepCopy()
			vcp.Spec.Sync.StorageClasses.Enabled = false
			err := k8sClient.Patch(ctx, vcp, client.MergeFrom(original))
			Expect(err).To(Not(HaveOccurred()))
		})

		It("will remove the synced storage classes from the virtual cluster", func() {
			Eventually(func(g Gomega) {
				hostStorageClasses, err := k8s.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
				g.Expect(err).To(Not(HaveOccurred()))

				for _, hostSC := range hostStorageClasses.Items {
					if hostSC.Labels[cluster.SyncEnabledLabelKey] == "true" {
						_, err := virtualCluster.Client.StorageV1().StorageClasses().Get(ctx, hostSC.Name, metav1.GetOptions{})
						g.Expect(err).To(HaveOccurred())
						g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
					}
				}
			}).
				WithPolling(time.Second).
				WithTimeout(time.Second * 60).
				Should(Succeed())
		})
	})
})
