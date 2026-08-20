package syncer_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/controller/syncer"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var PodDisruptionBudgetTests = func() {
	var (
		namespace string
		cluster   v1beta1.Cluster
	)

	BeforeEach(func() {
		ctx := context.Background()

		ns := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"},
		}
		err := hostTestEnv.k8sClient.Create(ctx, &ns)
		Expect(err).NotTo(HaveOccurred())

		namespace = ns.Name

		cluster = v1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cluster-",
				Namespace:    namespace,
			},
			Spec: v1beta1.ClusterSpec{
				Sync: &v1beta1.SyncConfig{
					// PDB syncing is opt-in (default off)
					PodDisruptionBudgets: v1beta1.PodDisruptionBudgetSyncConfig{
						Enabled: true,
					},
				},
			},
		}
		err = hostTestEnv.k8sClient.Create(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		err = syncer.AddPodDisruptionBudgetSyncer(ctx, virtManager, hostManager, cluster.Name, cluster.Namespace)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		ns := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := hostTestEnv.k8sClient.Delete(context.Background(), &ns)
		Expect(err).NotTo(HaveOccurred())
	})

	newPDB := func() *policyv1.PodDisruptionBudget {
		minAvailable := intstr.FromInt32(2)

		return &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "pdb-",
				Namespace:    "default",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Spec: policyv1.PodDisruptionBudgetSpec{
				MinAvailable: &minAvailable,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "web"},
				},
			},
		}
	}

	It("creates a pdb on the host cluster with a scoped selector", func() {
		ctx := context.Background()

		pdb := newPDB()
		err := virtTestEnv.k8sClient.Create(ctx, pdb)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created pdb %s in virtual cluster", pdb.Name))

		var hostPDB policyv1.PodDisruptionBudget

		hostPDBName := translateName(cluster, pdb.Namespace, pdb.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostPDBName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostPDB)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created pdb %s in host cluster", hostPDBName))

		// the original selector is preserved and scoped to the virtual
		// cluster and namespace, so it cannot match same-labeled pods of
		// other virtual namespaces sharing the host namespace
		Expect(hostPDB.Spec.Selector.MatchLabels).To(Equal(map[string]string{
			"app":                        "web",
			translate.ClusterNameLabel:   cluster.Name,
			translate.NamespaceNameLabel: pdb.Namespace,
		}))

		Expect(hostPDB.Spec.MinAvailable.IntValue()).To(Equal(2))
		Expect(hostPDB.Annotations[translate.ResourceNameAnnotation]).To(Equal(pdb.Name))
		Expect(hostPDB.Annotations[translate.ResourceNamespaceAnnotation]).To(Equal(pdb.Namespace))
	})

	It("scopes an empty selector to the virtual cluster and namespace", func() {
		ctx := context.Background()

		pdb := newPDB()
		pdb.Spec.Selector = nil

		err := virtTestEnv.k8sClient.Create(ctx, pdb)
		Expect(err).NotTo(HaveOccurred())

		var hostPDB policyv1.PodDisruptionBudget

		hostPDBName := translateName(cluster, pdb.Namespace, pdb.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostPDBName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostPDB)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		Expect(hostPDB.Spec.Selector.MatchLabels).To(Equal(map[string]string{
			translate.ClusterNameLabel:   cluster.Name,
			translate.NamespaceNameLabel: pdb.Namespace,
		}))
	})

	It("updates a pdb on the host cluster", func() {
		ctx := context.Background()

		pdb := newPDB()
		err := virtTestEnv.k8sClient.Create(ctx, pdb)
		Expect(err).NotTo(HaveOccurred())

		var hostPDB policyv1.PodDisruptionBudget

		hostPDBName := translateName(cluster, pdb.Namespace, pdb.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostPDBName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostPDB)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		key := client.ObjectKeyFromObject(pdb)
		err = virtTestEnv.k8sClient.Get(ctx, key, pdb)
		Expect(err).NotTo(HaveOccurred())

		newMinAvailable := intstr.FromInt32(1)
		pdb.Spec.MinAvailable = &newMinAvailable

		err = virtTestEnv.k8sClient.Update(ctx, pdb)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() int {
			key := client.ObjectKey{Name: hostPDBName, Namespace: namespace}
			err = hostTestEnv.k8sClient.Get(ctx, key, &hostPDB)
			Expect(err).NotTo(HaveOccurred())

			return hostPDB.Spec.MinAvailable.IntValue()
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(Equal(1))
	})

	It("deletes a pdb on the host cluster", func() {
		ctx := context.Background()

		pdb := newPDB()
		err := virtTestEnv.k8sClient.Create(ctx, pdb)
		Expect(err).NotTo(HaveOccurred())

		var hostPDB policyv1.PodDisruptionBudget

		hostPDBName := translateName(cluster, pdb.Namespace, pdb.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostPDBName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostPDB)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		err = virtTestEnv.k8sClient.Delete(ctx, pdb)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Deleted pdb %s in virtual cluster", pdb.Name))

		// the host pdb is cleaned up and the virtual pdb released
		// from its cleanup finalizer
		Eventually(func() bool {
			key := client.ObjectKey{Name: hostPDBName, Namespace: namespace}
			err := hostTestEnv.k8sClient.Get(ctx, key, &hostPDB)

			return apierrors.IsNotFound(err)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeTrue())

		Eventually(func() bool {
			key := client.ObjectKeyFromObject(pdb)
			err := virtTestEnv.k8sClient.Get(ctx, key, pdb)

			return apierrors.IsNotFound(err)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeTrue())
	})
}
