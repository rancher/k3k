package syncer_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/controller/syncer"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var SecretTests = func() {
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
					Secrets: v1alpha1.SecretSyncConfig{
						Enabled:         ptr.To(true),
						ActiveResources: ptr.To(false),
					},
				},
			},
		}
		err = hostTestEnv.k8sClient.Create(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		err = syncer.AddSecretSyncer(ctx, virtManager, hostManager, cluster.Name, cluster.Namespace)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		ns := v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := hostTestEnv.k8sClient.Delete(context.Background(), &ns)
		Expect(err).NotTo(HaveOccurred())
	})

	It("creates a Secret on the host cluster", func() {
		ctx := context.Background()

		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "secret-",
				Namespace:    "default",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Data: map[string][]byte{
				"foo": []byte("bar"),
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, secret)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created Secret %s in virtual cluster", secret.Name))

		var hostSecret v1.Secret
		hostSecretName := translateName(cluster, secret.Namespace, secret.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostSecretName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostSecret)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created Secret %s in host cluster", hostSecretName))

		Expect(hostSecret.Data).To(Equal(secret.Data))
		Expect(hostSecret.Labels).To(ContainElement("bar"))

		GinkgoWriter.Printf("labels: %v\n", hostSecret.Labels)
	})

	It("updates a Secret on the host cluster", func() {
		ctx := context.Background()

		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "secret-",
				Namespace:    "default",
			},
			Data: map[string][]byte{
				"foo": []byte("bar"),
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, secret)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created secret %s in virtual cluster", secret.Name))

		var hostSecret v1.Secret
		hostSecretName := translateName(cluster, secret.Namespace, secret.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostSecretName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostSecret)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created secret %s in host cluster", hostSecretName))

		Expect(hostSecret.Data).To(Equal(secret.Data))
		Expect(hostSecret.Labels).NotTo(ContainElement("bar"))

		key := client.ObjectKeyFromObject(secret)
		err = virtTestEnv.k8sClient.Get(ctx, key, secret)
		Expect(err).NotTo(HaveOccurred())

		secret.Labels = map[string]string{"foo": "bar"}

		// update virtual secret
		err = virtTestEnv.k8sClient.Update(ctx, secret)
		Expect(err).NotTo(HaveOccurred())
		Expect(secret.Labels).To(ContainElement("bar"))

		// check hostSecret
		Eventually(func() map[string]string {
			key := client.ObjectKey{Name: hostSecretName, Namespace: namespace}
			err = hostTestEnv.k8sClient.Get(ctx, key, &hostSecret)
			Expect(err).NotTo(HaveOccurred())
			return hostSecret.Labels
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(ContainElement("bar"))
	})

	It("deletes a secret on the host cluster", func() {
		ctx := context.Background()

		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "secret-",
				Namespace:    "default",
			},
			Data: map[string][]byte{
				"foo": []byte("bar"),
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, secret)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created secret %s in virtual cluster", secret.Name))

		var hostSecret v1.Secret
		hostSecretName := translateName(cluster, secret.Namespace, secret.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostSecretName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostSecret)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created secret %s in host cluster", hostSecretName))

		Expect(hostSecret.Data).To(Equal(hostSecret.Data))

		err = virtTestEnv.k8sClient.Delete(ctx, secret)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			key := client.ObjectKey{Name: hostSecretName, Namespace: namespace}
			err := hostTestEnv.k8sClient.Get(ctx, key, &hostSecret)
			return apierrors.IsNotFound(err)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeTrue())
	})
	It("will not create a secret on the host cluster if disabled", func() {
		ctx := context.Background()

		cluster.Spec.Sync.Secrets.Enabled = ptr.To(false)
		err := hostTestEnv.k8sClient.Update(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "secret-",
				Namespace:    "default",
			},
			Data: map[string][]byte{
				"foo": []byte("bar"),
			},
		}

		err = virtTestEnv.k8sClient.Create(ctx, secret)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created secret %s in virtual cluster", secret.Name))

		var hostSecret v1.Secret
		hostSecretName := translateName(cluster, secret.Namespace, secret.Name)

		Eventually(func() bool {
			key := client.ObjectKey{Name: hostSecretName, Namespace: namespace}
			err = hostTestEnv.k8sClient.Get(ctx, key, &hostSecret)
			return apierrors.IsNotFound(err)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeTrue())
	})
}
