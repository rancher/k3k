package k3k_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"k8s.io/client-go/kubernetes"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("In a shared cluster", Label(e2eTestLabel), Ordered, func() {
	var virtualCluster *VirtualCluster

	BeforeAll(func() {
		virtualCluster = NewVirtualCluster()

		DeferCleanup(func() {
			DeleteNamespaces(virtualCluster.Cluster.Namespace)
		})
	})

	When("creating a Pod with an invalid configuration", func() {
		var virtualPod *v1.Pod

		BeforeEach(func() {
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
									Name: "POD_NAME",
									ValueFrom: &v1.EnvVarSource{
										FieldRef: &v1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
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

				envVars := pod.Spec.Containers[0].Env
				g.Expect(envVars).NotTo(BeEmpty())

				var found bool
				for _, envVar := range envVars {
					if envVar.Name == "POD_NAME" {
						found = true

						g.Expect(envVars[0].ValueFrom).NotTo(BeNil())
						g.Expect(envVars[0].ValueFrom.FieldRef).NotTo(BeNil())
						g.Expect(envVars[0].ValueFrom.FieldRef.FieldPath).To(Equal("metadata.name"))
						break
					}
				}
				g.Expect(found).To(BeTrue())

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

				envVars := pod.Spec.Containers[0].Env
				g.Expect(envVars).NotTo(BeEmpty())

				var found bool
				for _, envVar := range envVars {
					if envVar.Name == "POD_NAME" {
						found = true

						g.Expect(envVar.ValueFrom).To(BeNil())
						g.Expect(envVar.Value).To(Equal(virtualPod.Name))
						break
					}
				}
				g.Expect(found).To(BeTrue())

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

				_, cond := GetPodCondition(&vPod.Status, v1.PodReady)
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

				_, cond := GetPodCondition(&hPod.Status, v1.PodReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})
	})

	When("installing the nginx-ingress controller", func() {
		BeforeAll(func() {
			By("installing the nginx-ingress controller")

			Expect(os.WriteFile("vk-kubeconfig.yaml", virtualCluster.Kubeconfig, 0o644)).To(Succeed())

			ingressNginx := "testdata/resources/ingress-nginx-v1.14.1.yaml"
			cmd := exec.Command("kubectl", "apply", "--kubeconfig", "vk-kubeconfig.yaml", "-f", ingressNginx)
			output, err := cmd.CombinedOutput()

			fmt.Println("#### output", "\n", string(output))

			Expect(err).NotTo(HaveOccurred(), string(output))
		})

		expectJobToSucceed := func(client *kubernetes.Clientset, namespace string, label string) {
			GinkgoHelper()

			Eventually(func(g Gomega) {
				listOpts := metav1.ListOptions{LabelSelector: "job-name=" + label}
				pods, err := client.CoreV1().Pods(namespace).List(context.Background(), listOpts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).NotTo(BeEmpty())

				g.Expect(pods.Items[0].Status.Phase).To(Equal(v1.PodSucceeded))
			}).
				WithPolling(time.Second).
				WithTimeout(2 * time.Minute).
				Should(Succeed())
		}

		It("should complete the ingress-nginx-admission-create Job in the host cluster", func() {
			expectJobToSucceed(k8s, virtualCluster.Cluster.Namespace, "ingress-nginx-admission-create")
		})

		It("should complete the ingress-nginx-admission-create Job in the virtual cluster", func() {
			expectJobToSucceed(virtualCluster.Client, "ingress-nginx", "ingress-nginx-admission-create")
		})

		It("should complete the ingress-nginx-admission-patch Job in the host cluster", func() {
			expectJobToSucceed(k8s, virtualCluster.Cluster.Namespace, "ingress-nginx-admission-patch")
		})

		It("should complete the ingress-nginx-admission-patch Job in the virtual cluster", func() {
			expectJobToSucceed(virtualCluster.Client, "ingress-nginx", "ingress-nginx-admission-patch")
		})

		It("should run the ingress-nginx controller", func() {
			Eventually(func(g Gomega) {
				ctx := context.Background()
				deployment, err := virtualCluster.Client.AppsV1().Deployments("ingress-nginx").Get(ctx, "ingress-nginx-controller", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				desiredReplicas := *deployment.Spec.Replicas

				status := deployment.Status
				g.Expect(status.ObservedGeneration).To(BeNumerically(">=", deployment.Generation))
				g.Expect(status.UpdatedReplicas).To(BeNumerically("==", desiredReplicas))
				g.Expect(status.AvailableReplicas).To(BeNumerically("==", desiredReplicas))
			})
		})
	})
})
