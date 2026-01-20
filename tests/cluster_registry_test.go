package k3k_test

import (
	"context"
	"os"
	"time"

	"k8s.io/kubernetes/pkg/api/v1/pod"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/policy"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a cluster with private registry configuration is used", Label("e2e"), Label(registryTestsLabel), func() {
	var virtualCluster *VirtualCluster
	BeforeEach(func() {
		ctx := context.Background()

		vcp := &v1beta1.VirtualClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "policy-",
			},
			Spec: v1beta1.VirtualClusterPolicySpec{
				AllowedMode:          v1beta1.VirtualClusterMode,
				DisableNetworkPolicy: true,
			},
		}
		Expect(k8sClient.Create(ctx, vcp)).To(Succeed())

		namespace := NewNamespace()

		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(namespace), namespace)
		Expect(err).To(Not(HaveOccurred()))

		namespace.Labels = map[string]string{
			policy.PolicyNameLabelKey: vcp.Name,
		}
		Expect(k8sClient.Update(ctx, namespace)).To(Succeed())

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
			Expect(k8sClient.Delete(ctx, vcp)).To(Succeed())
		})

		err = privateRegistry(ctx, namespace.Name)
		Expect(err).ToNot(HaveOccurred())

		cluster := NewCluster(namespace.Name)

		// configure the cluster with the private registry secrets using SecretMounts
		// Using subPath allows mounting individual files while keeping parent directories writable
		cluster.Spec.SecretMounts = []v1beta1.SecretMount{
			{
				SecretVolumeSource: v1.SecretVolumeSource{
					SecretName: "k3s-registry-config",
				},
				MountPath: "/etc/rancher/k3s/registries.yaml",
				SubPath:   "registries.yaml",
			},
			{
				SecretVolumeSource: v1.SecretVolumeSource{
					SecretName: "private-registry-ca-cert",
				},
				MountPath: "/etc/rancher/k3s/tls/ca.crt",
				SubPath:   "tls.crt",
			},
		}

		cluster.Spec.Mode = v1beta1.VirtualClusterMode

		// airgap the k3k-server pod
		err = buildRegistryNetPolicy(ctx, cluster.Namespace)
		Expect(err).ToNot(HaveOccurred())

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})

	It("will be load the registries.yaml and crts in server pod", func() {
		ctx := context.Background()

		labelSelector := "cluster=" + virtualCluster.Cluster.Name + ",role=server"
		serverPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))

		Expect(len(serverPods.Items)).To(Equal(1))
		serverPod := serverPods.Items[0]

		// check registries.yaml
		registriesConfigPath := "/etc/rancher/k3s/registries.yaml"
		registriesConfig, err := readFileWithinPod(ctx, k8s, restcfg, serverPod.Name, serverPod.Namespace, registriesConfigPath)
		Expect(err).To(Not(HaveOccurred()))

		registriesConfigTestFile, err := os.ReadFile("testdata/registry/registries.yaml")
		Expect(err).To(Not(HaveOccurred()))
		Expect(registriesConfig).To(Equal(registriesConfigTestFile))

		// check ca.crt
		CACrtPath := "/etc/rancher/k3s/tls/ca.crt"
		CACrt, err := readFileWithinPod(ctx, k8s, restcfg, serverPod.Name, serverPod.Namespace, CACrtPath)
		Expect(err).To(Not(HaveOccurred()))

		CACrtTestFile, err := os.ReadFile("testdata/registry/certs/ca.crt")
		Expect(err).To(Not(HaveOccurred()))
		Expect(CACrt).To(Equal(CACrtTestFile))
	})
	It("will only pull images from mirrored docker.io registry", func() {
		ctx := context.Background()

		// make sure that any pod using docker.io mirror works
		virtualCluster.NewNginxPod("")

		// creating a pod with image that uses any registry other than docker.io should fail
		// for example public.ecr.aws/docker/library/alpine:latest
		alpinePod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "alpine-",
				Namespace:    "default",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{{
					Name:  "alpine",
					Image: "public.ecr.aws/docker/library/alpine:latest",
				}},
			},
		}

		By("Creating Alpine Pod and making sure its failing to start")
		var err error
		alpinePod, err = virtualCluster.Client.CoreV1().Pods(alpinePod.Namespace).Create(ctx, alpinePod, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// check that the alpine Pod is failing to pull the image
		Eventually(func(g Gomega) {
			alpinePod, err = virtualCluster.Client.CoreV1().Pods(alpinePod.Namespace).Get(ctx, alpinePod.Name, metav1.GetOptions{})
			g.Expect(err).To(Not(HaveOccurred()))

			status, _ := pod.GetContainerStatus(alpinePod.Status.ContainerStatuses, "alpine")
			state := status.State.Waiting
			g.Expect(state).NotTo(BeNil())

			g.Expect(state.Reason).To(BeEquivalentTo("ImagePullBackOff"))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})
})
