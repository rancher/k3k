package k3k_test

import (
	"context"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	addonsTestsLabel             = "addons"
	addonsSecretName             = "k3s-addons"
	secretMountManifestMountPath = "/var/lib/rancher/k3s/server/manifests/nginx.yaml"
	addonManifestMountPath       = "/var/lib/rancher/k3s/server/manifests/k3s-addons/nginx.yaml"
)

var _ = When("a cluster with secretMounts configuration is used to load addons", Label("e2e"), Label(addonsTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		ctx := context.Background()

		namespace := NewNamespace()

		// Create the addon secret
		err := createAddonSecret(ctx, namespace.Name)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		cluster.Spec.SecretMounts = []v1beta1.SecretMount{
			{
				SecretVolumeSource: v1.SecretVolumeSource{
					SecretName: addonsSecretName,
				},
				MountPath: secretMountManifestMountPath,
				SubPath:   "nginx.yaml",
			},
		}

		CreateCluster(cluster)

		virtualClient, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     virtualClient,
		}
	})

	It("will load the addon manifest in server pod", func() {
		ctx := context.Background()

		serverPods := listServerPods(ctx, virtualCluster)

		Expect(len(serverPods)).To(Equal(1))
		serverPod := serverPods[0]

		addonContent, err := readFileWithinPod(ctx, k8s, restcfg, serverPod.Name, serverPod.Namespace, secretMountManifestMountPath)
		Expect(err).To(Not(HaveOccurred()))

		addonTestFile, err := os.ReadFile("testdata/addons/nginx.yaml")
		Expect(err).To(Not(HaveOccurred()))
		Expect(addonContent).To(Equal(addonTestFile))
	})

	It("will deploy the addon pod in the virtual cluster", func() {
		ctx := context.Background()

		Eventually(func(g Gomega) {
			nginxPod, err := virtualCluster.Client.CoreV1().Pods("default").Get(ctx, "nginx-addon", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(nginxPod.Status.Phase).To(Equal(v1.PodRunning))
		}).
			WithTimeout(time.Minute * 3).
			WithPolling(time.Second * 5).
			Should(Succeed())
	})
})

var _ = When("a cluster with addon configuration is used with addons secret in the same namespace", Label("e2e"), Label(addonsTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		ctx := context.Background()

		namespace := NewNamespace()

		// Create the addon secret
		err := createAddonSecret(ctx, namespace.Name)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		cluster.Spec.Addons = []v1beta1.Addon{
			{
				SecretNamespace: namespace.Name,
				SecretRef:       addonsSecretName,
			},
		}

		CreateCluster(cluster)

		virtualClient, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     virtualClient,
		}

		serverPods := listServerPods(ctx, virtualCluster)

		Expect(len(serverPods)).To(Equal(1))
		serverPod := serverPods[0]

		addonContent, err := readFileWithinPod(ctx, k8s, restcfg, serverPod.Name, serverPod.Namespace, addonManifestMountPath)
		Expect(err).To(Not(HaveOccurred()))

		addonTestFile, err := os.ReadFile("testdata/addons/nginx.yaml")
		Expect(err).To(Not(HaveOccurred()))
		Expect(addonContent).To(Equal(addonTestFile))

		Eventually(func(g Gomega) {
			nginxPod, err := virtualCluster.Client.CoreV1().Pods("default").Get(ctx, "nginx-addon", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(nginxPod.Status.Phase).To(Equal(v1.PodRunning))
		}).
			WithTimeout(time.Minute * 3).
			WithPolling(time.Second * 5).
			Should(Succeed())
	})
})

var _ = When("a cluster with addon configuration is used with addons secret in the different namespace", Label("e2e"), Label(addonsTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		ctx := context.Background()

		namespace := NewNamespace()
		secretNamespace := NewNamespace()

		// Create the addon secret
		err := createAddonSecret(ctx, secretNamespace.Name)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name, secretNamespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		cluster.Spec.Addons = []v1beta1.Addon{
			{
				SecretNamespace: secretNamespace.Name,
				SecretRef:       addonsSecretName,
			},
		}

		CreateCluster(cluster)

		virtualClient, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     virtualClient,
		}
	})

	It("will load the addon manifest in server pod and deploys the pod", func() {
		ctx := context.Background()

		serverPods := listServerPods(ctx, virtualCluster)

		Expect(len(serverPods)).To(Equal(1))
		serverPod := serverPods[0]

		addonContent, err := readFileWithinPod(ctx, k8s, restcfg, serverPod.Name, serverPod.Namespace, addonManifestMountPath)
		Expect(err).To(Not(HaveOccurred()))

		addonTestFile, err := os.ReadFile("testdata/addons/nginx.yaml")
		Expect(err).To(Not(HaveOccurred()))
		Expect(addonContent).To(Equal(addonTestFile))

		Eventually(func(g Gomega) {
			nginxPod, err := virtualCluster.Client.CoreV1().Pods("default").Get(ctx, "nginx-addon", metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(nginxPod.Status.Phase).To(Equal(v1.PodRunning))
		}).
			WithTimeout(time.Minute * 3).
			WithPolling(time.Second * 5).
			Should(Succeed())
	})
})

func createAddonSecret(ctx context.Context, namespace string) error {
	addonContent, err := os.ReadFile("testdata/addons/nginx.yaml")
	if err != nil {
		return err
	}

	secret := &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      addonsSecretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"nginx.yaml": addonContent,
		},
	}

	return k8sClient.Create(ctx, secret)
}
