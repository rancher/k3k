package k3k_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/api/v1/pod"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/controller"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("In a shared cluster", Label(podTestsLabel), Ordered, func() {
	var (
		virtualCluster *VirtualCluster
		translator     *translate.ToHostTranslator
	)

	BeforeAll(func() {
		virtualCluster = NewVirtualCluster()
		translator = translate.NewHostTranslator(virtualCluster.Cluster)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})
	})

	When("creating a Pod without any Affinity", func() {
		var pod *corev1.Pod

		BeforeAll(func() {
			var err error

			ctx := context.Background()

			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "nginx-",
					Namespace:    "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx",
					}},
				},
			}

			pod, err = virtualCluster.Client.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("should have the default Affinity", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				hostPodName := translator.NamespacedName(pod)

				hostPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostPod.Spec.Affinity).To(Not(BeNil()))
				g.Expect(hostPod.Spec.Affinity.NodeAffinity).To(Not(BeNil()))
				g.Expect(hostPod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(Not(BeNil()))

				preferredScheduling := hostPod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
				g.Expect(preferredScheduling).To(Not(BeEmpty()))
				g.Expect(preferredScheduling[0].Weight).To(Equal(int32(100)))
				g.Expect(preferredScheduling[0].Preference.MatchExpressions).To(Not(BeEmpty()))
				g.Expect(preferredScheduling[0].Preference.MatchExpressions[0].Key).To(Equal("kubernetes.io/hostname"))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})
	})

	When("creating a Pod with an Affinity", func() {
		var pod *corev1.Pod

		BeforeAll(func() {
			var err error

			ctx := context.Background()

			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "nginx-",
					Namespace:    "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx",
					}},
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{{
									MatchExpressions: []corev1.NodeSelectorRequirement{{
										Key:      "kubernetes.io/hostname",
										Operator: corev1.NodeSelectorOpNotIn,
										Values:   []string{"fake"},
									}},
								}},
							},
						},
					},
				},
			}

			pod, err = virtualCluster.Client.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("should not have the default Affinity", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				hostPodName := translator.NamespacedName(pod)

				hostPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostPod.Spec.Affinity).To(Not(BeNil()))
				g.Expect(hostPod.Spec.Affinity.NodeAffinity).To(Not(BeNil()))
				g.Expect(hostPod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution).To(Not(BeNil()))

				requiredScheduling := hostPod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
				g.Expect(requiredScheduling).To(Not(BeNil()))
				g.Expect(requiredScheduling.NodeSelectorTerms).To(Not(BeEmpty()))
				g.Expect(requiredScheduling.NodeSelectorTerms[0].MatchExpressions).To(Not(BeEmpty()))
				g.Expect(requiredScheduling.NodeSelectorTerms[0].MatchExpressions[0].Key).To(Equal("kubernetes.io/hostname"))
				g.Expect(requiredScheduling.NodeSelectorTerms[0].MatchExpressions[0].Values).To(ContainElement("fake"))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})
	})

	When("creating a Pod with an invalid configuration", func() {
		var virtualPod *corev1.Pod

		BeforeEach(func() {
			p := &corev1.Pod{
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
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx",
							Env: []corev1.EnvVar{
								{
									Name: "POD_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
								},
								{
									Name: "ANNOTATION",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.annotations['mysubpath']",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
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
					Volumes: []corev1.Volume{{
						Name:         "workdir",
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
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

				g.Expect(pod.Status.Phase).To(Equal(corev1.PodPending))

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
				hostPodName := translator.NamespacedName(virtualPod)

				pod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(pod.Status.Phase).To(Equal(corev1.PodPending))

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

				_, cond := pod.GetPodCondition(&vPod.Status, corev1.PodReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())

			By("Checking the status of the Pod in the Host Cluster")

			Eventually(func(g Gomega) {
				hostPodName := translator.NamespacedName(virtualPod)

				hPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				_, cond := pod.GetPodCondition(&hPod.Status, corev1.PodReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})
	})

	When("updating the labels of a synced Pod", func() {
		var virtualPod *corev1.Pod

		BeforeEach(func(ctx context.Context) {
			p := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "nginx-",
					Namespace:    "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx",
					}},
				},
			}

			var err error

			virtualPod, err = virtualCluster.Client.CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("should keep the clusterName isolation label on the host Pod", func(ctx context.Context) {
			hostPodName := translator.NamespacedName(virtualPod)

			By("Checking the host Pod carries the clusterName isolation label")

			Eventually(func(g Gomega) {
				hostPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostPod.Labels).To(HaveKeyWithValue(translate.ClusterNameLabel, virtualCluster.Cluster.Name))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())

			By("Updating a label on the virtual Pod")

			var err error

			virtualPod, err = virtualCluster.Client.CoreV1().Pods(virtualPod.Namespace).Get(ctx, virtualPod.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			if virtualPod.Labels == nil {
				virtualPod.Labels = map[string]string{}
			}

			virtualPod.Labels["k3k.io/test"] = "updated"

			virtualPod, err = virtualCluster.Client.CoreV1().Pods(virtualPod.Namespace).Update(ctx, virtualPod, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Checking the host Pod still carries the clusterName isolation label after the update")

			// The label must survive the update: it is what the isolation NetworkPolicy selects on
			// (podSelector matchExpressions: clusterName Exists). If it is dropped, the synced
			// workload pod escapes isolation.
			Eventually(func(g Gomega) {
				hostPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostPod.Labels).To(HaveKeyWithValue("k3k.io/test", "updated"))
				g.Expect(hostPod.Labels).To(HaveKeyWithValue(translate.ClusterNameLabel, virtualCluster.Cluster.Name))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})
	})

	When("creating a Pod with downward API variables in environment variable", func() {
		var virtualPod *corev1.Pod

		BeforeEach(func() {
			ctx := context.Background()

			var err error

			p := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "nginx-",
					Namespace:    "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx",
							Env: []corev1.EnvVar{
								{
									Name: "POD_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
								},
								{
									Name: "STATUS_POD_IP",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "status.podIP",
										},
									},
								},
							},
						},
					},
				},
			}

			virtualPod, err = virtualCluster.Client.CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("should be scheduled and running in the virtual cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				pod, err := virtualCluster.Client.CoreV1().Pods(virtualPod.Namespace).Get(ctx, virtualPod.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
				g.Expect(pod.Status.PodIP).NotTo(BeEmpty())
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})

		It("should be scheduled and running in the host cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				translator := translate.NewHostTranslator(virtualCluster.Cluster)
				hostPodName := translator.NamespacedName(virtualPod)

				pod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
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

				g.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodSucceeded))
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

	When("creating a Pod with a projected serviceaccount token", Label("test"), func() {
		var (
			pod                     *corev1.Pod
			serviceAccountName      = "sa-nginx"
			serviceAccountTokenExp  = int64(3600)
			serviceAccountTokenPath = "token"
		)

		BeforeAll(func() {
			var err error

			ctx := context.Background()

			serviceaccount := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceAccountName,
					Namespace: "default",
				},
			}
			serviceaccount, err = virtualCluster.Client.CoreV1().ServiceAccounts(serviceaccount.Namespace).Create(ctx, serviceaccount, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "nginx-",
					Namespace:    "default",
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: new(false),
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "projected-serviceaccount-token-vol",
								MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
							},
						},
					}},
					ServiceAccountName: serviceaccount.Name,
					Volumes: []corev1.Volume{
						{
							Name: "projected-serviceaccount-token-vol",
							VolumeSource: corev1.VolumeSource{
								Projected: &corev1.ProjectedVolumeSource{
									Sources: []corev1.VolumeProjection{
										{
											ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
												Audience:          "https://kubernetes.default.svc.cluster.local",
												ExpirationSeconds: new(serviceAccountTokenExp),
												Path:              serviceAccountTokenPath,
											},
										},
									},
								},
							},
						},
					},
				},
			}

			pod, err = virtualCluster.Client.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("should have translated projected serviceaccount token to secret", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				hostPodName := translator.NamespacedName(pod)

				virtualSecretName := controller.SafeConcatNameWithPrefix([]string{serviceAccountName, strconv.FormatInt(serviceAccountTokenExp, 10), serviceAccountTokenPath}...)
				hostSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      virtualSecretName,
						Namespace: pod.Namespace,
					},
				}
				// there is no way to get the result of the token request from the API
				translator.TranslateTo(hostSecret)

				hostPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostPod.Spec.Volumes).To(HaveLen(1))
				g.Expect(hostPod.Spec.Volumes[0].Name).To(Equal("projected-serviceaccount-token-vol"))
				g.Expect(hostPod.Spec.Volumes[0].Projected).To(Not(BeNil()))
				g.Expect(hostPod.Spec.Volumes[0].Projected.Sources).To(HaveLen(1))
				g.Expect(hostPod.Spec.Volumes[0].Projected.Sources[0].Secret).To(Not(BeNil()))
				g.Expect(hostPod.Spec.Volumes[0].Projected.Sources[0].ServiceAccountToken).To(BeNil())
				g.Expect(hostPod.Spec.Volumes[0].Projected.Sources[0].Secret.Name).To(Equal(hostSecret.Name))

				// verifying that the secret is created on the host cluster
				hostSecret, err = k8s.CoreV1().Secrets(hostPodName.Namespace).Get(ctx, hostSecret.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostSecret.Data).NotTo(BeNil())
				g.Expect(hostSecret.Data[serviceAccountTokenPath]).NotTo(BeEmpty())
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})
	})
})
