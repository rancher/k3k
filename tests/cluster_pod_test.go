package k3k_test

import (
	"context"
	"time"

	"k8s.io/kubernetes/pkg/api/v1/pod"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a cluster creates a pod with an invalid configuration", Label(e2eTestLabel), func() {
	var (
		virtualCluster *VirtualCluster
		virtualPod     *v1.Pod
	)

	// This BeforeEach/AfterEach will create a new namespace and a default policy for each test.
	BeforeEach(func() {
		virtualCluster = NewVirtualCluster()

		DeferCleanup(func() {
			DeleteNamespaces(virtualCluster.Cluster.Namespace)
		})

		p := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "nginx-",
				Namespace:    "default",
				Labels: map[string]string{
					"name": "var-expansion-test",
				},
				Annotations: map[string]string{
					"notmysubpath": "mypath",
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "nginx",
						Image: "nginx",
						Env: []v1.EnvVar{
							{
								Name:  "POD_NAME",
								Value: "nginx",
							},
							{
								Name: "ANNOTATION",
								ValueFrom: &v1.EnvVarSource{
									FieldRef: &v1.ObjectFieldSelector{
										FieldPath: "metadata.annotations['mysubpath']",
									},
								},
							},
						},
						VolumeMounts: []v1.VolumeMount{
							{
								Name:      "workdir",
								MountPath: "/volume_mount",
							},
							{
								Name:        "workdir",
								MountPath:   "/subpath_mount",
								SubPathExpr: "$(ANNOTATION)/$(POD_NAME)",
							},
						},
					},
				},
				Volumes: []v1.Volume{{
					Name:         "workdir",
					VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}},
				}},
			},
		}

		ctx := context.Background()
		var err error

		virtualPod, err = virtualCluster.Client.CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))
	})

	It("should be in Pending status with the CreateContainerConfigError until we fix the annotation", func() {
		ctx := context.Background()

		By("Checking the container status of the Pod in the Virtual Cluster")

		Eventually(func(g Gomega) {
			pod, err := virtualCluster.Client.CoreV1().Pods(virtualPod.Namespace).Get(ctx, virtualPod.Name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(pod.Status.Phase).To(Equal(v1.PodPending))

			containerStatuses := pod.Status.ContainerStatuses
			g.Expect(containerStatuses).To(HaveLen(1))

			waitingState := containerStatuses[0].State.Waiting
			g.Expect(waitingState).NotTo(BeNil())
			g.Expect(waitingState.Reason).To(Equal("CreateContainerConfigError"))
		}).
			WithPolling(time.Second).
			WithTimeout(time.Minute).
			Should(Succeed())

		By("Checking the container status of the Pod in the Host Cluster")

		Eventually(func(g Gomega) {
			translator := translate.NewHostTranslator(virtualCluster.Cluster)
			hostPodName := translator.NamespacedName(virtualPod)

			pod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(pod.Status.Phase).To(Equal(v1.PodPending))

			containerStatuses := pod.Status.ContainerStatuses
			g.Expect(containerStatuses).To(HaveLen(1))

			waitingState := containerStatuses[0].State.Waiting
			g.Expect(waitingState).NotTo(BeNil())
			g.Expect(waitingState.Reason).To(Equal("CreateContainerConfigError"))
		}).
			WithPolling(time.Second).
			WithTimeout(time.Minute).
			Should(Succeed())

		By("Fixing the annotation")

		var err error

		virtualPod, err = virtualCluster.Client.CoreV1().Pods(virtualPod.Namespace).Get(ctx, virtualPod.Name, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		virtualPod.Annotations["mysubpath"] = virtualPod.Annotations["notmysubpath"]
		delete(virtualPod.Annotations, "notmysubpath")

		virtualPod, err = virtualCluster.Client.CoreV1().Pods(virtualPod.Namespace).Update(ctx, virtualPod, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the status of the Pod in the Virtual Cluster")

		Eventually(func(g Gomega) {
			vPod, err := virtualCluster.Client.CoreV1().Pods(virtualPod.Namespace).Get(ctx, virtualPod.Name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())

			_, cond := pod.GetPodCondition(&vPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}).
			WithPolling(time.Second).
			WithTimeout(time.Minute).
			Should(Succeed())

		By("Checking the status of the Pod in the Host Cluster")

		Eventually(func(g Gomega) {
			translator := translate.NewHostTranslator(virtualCluster.Cluster)
			hostPodName := translator.NamespacedName(virtualPod)

			hPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())

			_, cond := pod.GetPodCondition(&hPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}).
			WithPolling(time.Second).
			WithTimeout(time.Minute).
			Should(Succeed())
	})
})
