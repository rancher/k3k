package syncer_test

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/controller/syncer"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var ConfigMapTests = func() {
	var (
		namespace string
		cluster   v1beta1.Cluster
	)

	BeforeEach(func() {
		ctx := context.Background()

		ns := v1.Namespace{
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
					ConfigMaps: v1beta1.ConfigMapSyncConfig{
						Enabled: true,
					},
				},
			},
		}
		err = hostTestEnv.k8sClient.Create(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		err = syncer.AddConfigMapSyncer(ctx, virtManager, hostManager, cluster.Name, cluster.Namespace)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		ns := v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := hostTestEnv.k8sClient.Delete(context.Background(), &ns)
		Expect(err).NotTo(HaveOccurred())
	})

	It("creates a ConfigMap on the host cluster", func() {
		ctx := context.Background()

		configMap := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cm-",
				Namespace:    "default",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Data: map[string]string{
				"foo": "bar",
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created configmap %s in virtual cluster", configMap.Name))

		var hostConfigMap v1.ConfigMap
		hostConfigMapName := translateName(cluster, configMap.Namespace, configMap.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostConfigMapName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostConfigMap)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created Configmap %s in host cluster", hostConfigMapName))

		Expect(hostConfigMap.Data).To(Equal(configMap.Data))
		Expect(hostConfigMap.Labels).To(ContainElement("bar"))

		GinkgoWriter.Printf("labels: %v\n", hostConfigMap.Labels)
	})

	It("updates a ConfigMap on the host cluster", func() {
		ctx := context.Background()

		configMap := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cm-",
				Namespace:    "default",
			},
			Data: map[string]string{
				"foo": "bar",
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created configmap %s in virtual cluster", configMap.Name))

		var hostConfigMap v1.ConfigMap
		hostConfigMapName := translateName(cluster, configMap.Namespace, configMap.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostConfigMapName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostConfigMap)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created configmap %s in host cluster", hostConfigMapName))

		Expect(hostConfigMap.Data).To(Equal(configMap.Data))
		Expect(hostConfigMap.Labels).NotTo(ContainElement("bar"))

		key := client.ObjectKeyFromObject(configMap)
		err = virtTestEnv.k8sClient.Get(ctx, key, configMap)
		Expect(err).NotTo(HaveOccurred())

		configMap.Labels = map[string]string{"foo": "bar"}

		// update virtual configmap
		err = virtTestEnv.k8sClient.Update(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap.Labels).To(ContainElement("bar"))

		err = virtTestEnv.k8sClient.Get(ctx, key, configMap)

		// check hostConfigMap
		Eventually(func() map[string]string {
			key := client.ObjectKey{Name: hostConfigMapName, Namespace: namespace}
			err = hostTestEnv.k8sClient.Get(ctx, key, &hostConfigMap)
			Expect(err).NotTo(HaveOccurred())
			return hostConfigMap.Labels
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(ContainElement("bar"))
	})

	It("deletes a configMap on the host cluster", func() {
		ctx := context.Background()

		configMap := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cm-",
				Namespace:    "default",
			},
			Data: map[string]string{
				"foo": "bar",
			},
		}

		err := virtTestEnv.k8sClient.Create(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created configmap %s in virtual cluster", configMap.Name))

		var hostConfigMap v1.ConfigMap
		hostConfigMapName := translateName(cluster, configMap.Namespace, configMap.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostConfigMapName, Namespace: namespace}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostConfigMap)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created configmap %s in host cluster", hostConfigMapName))

		Expect(hostConfigMap.Data).To(Equal(hostConfigMap.Data))

		err = virtTestEnv.k8sClient.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			key := client.ObjectKey{Name: hostConfigMapName, Namespace: namespace}
			err := hostTestEnv.k8sClient.Get(ctx, key, &hostConfigMap)
			return apierrors.IsNotFound(err)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeTrue())
	})
	It("will not sync a configMap if disabled", func() {
		ctx := context.Background()

		cluster.Spec.Sync.ConfigMaps.Enabled = false
		err := hostTestEnv.k8sClient.Update(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		configMap := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cm-",
				Namespace:    "default",
			},
			Data: map[string]string{
				"foo": "bar",
			},
		}

		err = virtTestEnv.k8sClient.Create(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created configmap %s in virtual cluster", configMap.Name))

		var hostConfigMap v1.ConfigMap
		hostConfigMapName := translateName(cluster, configMap.Namespace, configMap.Name)

		Eventually(func() bool {
			key := client.ObjectKey{Name: hostConfigMapName, Namespace: namespace}
			err := hostTestEnv.k8sClient.Get(ctx, key, &hostConfigMap)
			GinkgoWriter.Printf("error: %v", err)
			return apierrors.IsNotFound(err)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeTrue())
	})
}
