package e2e_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/api/v1/pod"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/provider"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("In a shared cluster", Label(podTestsLabel), Ordered, func() {
	var (
		virtualCluster *VirtualCluster
		translator     *translate.ToHostTranslator
	)

	BeforeAll(func() {
		virtualCluster = NewVirtualClusterWithOpts(func(c *v1beta1.Cluster) {
			c.Spec.AllowedHostSchedulers = []string{"custom-scheduler"}
		})
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

	When("creating a Pod with a projected serviceaccount token", func() {
		var (
			pod                         *corev1.Pod
			serviceAccountName          = "sa-nginx"
			serviceAccountTokenExp      = int64(3600)
			serviceAccountTokenPath     = "token"
			serviceAccountTokenAudience = "https://kubernetes.default.svc.cluster.local"
		)

		BeforeAll(func(ctx context.Context) {
			var err error

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
												Audience:          serviceAccountTokenAudience,
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

		It("should have translated projected serviceaccount token to secret", func(ctx context.Context) {
			Eventually(func(g Gomega) {
				hostPodName := translator.NamespacedName(pod)

				hostPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostPod.Spec.Volumes).To(HaveLen(1))
				g.Expect(hostPod.Spec.Volumes[0].Name).To(Equal("projected-serviceaccount-token-vol"))
				g.Expect(hostPod.Spec.Volumes[0].Projected).To(Not(BeNil()))
				g.Expect(hostPod.Spec.Volumes[0].Projected.Sources).To(HaveLen(1))
				g.Expect(hostPod.Spec.Volumes[0].Projected.Sources[0].Secret).To(Not(BeNil()))
				g.Expect(hostPod.Spec.Volumes[0].Projected.Sources[0].ServiceAccountToken).To(BeNil())

				// verifying that the secret is created on the host cluster
				hostSecretName := hostPod.Spec.Volumes[0].Projected.Sources[0].Secret.Name
				hostSecret, err := k8s.CoreV1().Secrets(hostPodName.Namespace).Get(ctx, hostSecretName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostSecret.Data).NotTo(BeNil())
				g.Expect(hostSecret.Data[serviceAccountTokenPath]).NotTo(BeEmpty())
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})
	})

	When("creating a Pod with the default serviceaccount token", func() {
		var virtualPod *corev1.Pod

		BeforeAll(func(ctx context.Context) {
			var err error

			virtualPod = &corev1.Pod{
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

			virtualPod, err = virtualCluster.Client.CoreV1().Pods(virtualPod.Namespace).Create(ctx, virtualPod, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("should mount a token bound to the virtual Pod", func(ctx context.Context) {
			hostPodName := translator.NamespacedName(virtualPod)

			Eventually(func(g Gomega) {
				hostPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				var hostSecretName string

				for _, volume := range hostPod.Spec.Volumes {
					if strings.HasPrefix(volume.Name, controller.SafeConcatNameWithPrefix("kube-api-access")) && volume.Secret != nil {
						hostSecretName = volume.Secret.SecretName
					}
				}

				g.Expect(hostSecretName).NotTo(BeEmpty())

				hostSecret, err := k8s.CoreV1().Secrets(hostPodName.Namespace).Get(ctx, hostSecretName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostSecret.Data).To(HaveKey("ca.crt"))
				g.Expect(hostSecret.Data).To(HaveKeyWithValue("namespace", []byte(virtualPod.Namespace)))

				// the secret is garbage collected with the host pod
				g.Expect(hostSecret.OwnerReferences).To(ContainElement(HaveField("UID", hostPod.UID)))

				// a bound token has an expiration, an audience and a reference to the pod
				tokenParts := strings.Split(string(hostSecret.Data["token"]), ".")
				g.Expect(tokenParts).To(HaveLen(3))

				payload, err := base64.RawURLEncoding.DecodeString(tokenParts[1])
				g.Expect(err).NotTo(HaveOccurred())

				var claims map[string]any

				g.Expect(json.Unmarshal(payload, &claims)).To(Succeed())
				g.Expect(claims).To(HaveKey("exp"))
				g.Expect(claims).To(HaveKeyWithValue("aud", Not(BeEmpty())))
				g.Expect(claims).To(HaveKeyWithValue("kubernetes.io", HaveKeyWithValue("pod", And(
					HaveKeyWithValue("name", virtualPod.Name),
					HaveKeyWithValue("uid", string(virtualPod.UID)),
				))))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})

		It("should not create a legacy serviceaccount token secret", func(ctx context.Context) {
			secrets, err := virtualCluster.Client.CoreV1().Secrets(virtualPod.Namespace).List(ctx, metav1.ListOptions{
				FieldSelector: "type=" + string(corev1.SecretTypeServiceAccountToken),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(secrets.Items).To(BeEmpty())
		})
	})

	When("creating a Pod with the host scheduler annotation", func() {
		newPod := func(ctx context.Context, annotations map[string]string) *corev1.Pod {
			GinkgoHelper()

			// create a new namespace for each virtual pod
			ns, err := virtualCluster.Client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "custom-scheduler-",
				},
			}, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			p := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "nginx-",
					Namespace:    ns.Name,
					Annotations:  annotations,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx",
					}},
				},
			}

			p, err = virtualCluster.Client.CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			return p
		}

		It("should use the annotated scheduler on the host Pod when it is allowed", func(ctx context.Context) {
			virtualPod := newPod(ctx, map[string]string{provider.HostSchedulerNameAnnotation: "custom-scheduler"})
			hostPodName := translator.NamespacedName(virtualPod)

			Eventually(func(g Gomega) {
				hostPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostPod.Spec.SchedulerName).To(Equal("custom-scheduler"))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})

		It("should not create the host Pod when the annotated scheduler is not allowed", func(ctx context.Context) {
			virtualPod := newPod(ctx, map[string]string{provider.HostSchedulerNameAnnotation: "not-allowed-scheduler"})

			Eventually(func(g Gomega) {
				eventList, err := virtualCluster.Client.CoreV1().Events(virtualPod.Namespace).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(eventList.Items) > 0).To(BeTrue())

				var providerFailedEvent *corev1.Event

				for _, event := range eventList.Items {
					if event.Type == corev1.EventTypeWarning && event.Reason == "ProviderCreateFailed" {
						providerFailedEvent = &event
						break
					}
				}

				g.Expect(providerFailedEvent).NotTo(BeNil())
				g.Expect(providerFailedEvent.Message).To(ContainSubstring(`scheduler "not-allowed-scheduler" is not allowed for this cluster`))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})

		It("should use the default scheduler on the host Pod without the annotation", func(ctx context.Context) {
			virtualPod := newPod(ctx, nil)
			hostPodName := translator.NamespacedName(virtualPod)

			Eventually(func(g Gomega) {
				hostPod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostPod.Spec.SchedulerName).To(Equal(corev1.DefaultSchedulerName))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})

		It("should not create the host pod when the allowed custom schedulers is empty and the pod is annotated", func(ctx context.Context) {
			origCluster := virtualCluster.Cluster.DeepCopy()
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(origCluster), origCluster)
			Expect(err).ToNot(HaveOccurred())

			origCluster.Spec.AllowedHostSchedulers = nil
			err = k8sClient.Update(ctx, origCluster)
			Expect(err).ToNot(HaveOccurred())

			virtualPod := newPod(ctx, map[string]string{provider.HostSchedulerNameAnnotation: "empty-allowed-schedulers"})

			Eventually(func(g Gomega) {
				eventList, err := virtualCluster.Client.CoreV1().Events(virtualPod.Namespace).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(eventList.Items) > 0).To(BeTrue())

				var providerFailedEvent *corev1.Event

				for _, event := range eventList.Items {
					if event.Type == corev1.EventTypeWarning && event.Reason == "ProviderCreateFailed" {
						providerFailedEvent = &event
						break
					}
				}

				g.Expect(providerFailedEvent).NotTo(BeNil())
				g.Expect(providerFailedEvent.Message).To(ContainSubstring(`scheduler "empty-allowed-schedulers" is not allowed for this cluster`))
			}).
				WithPolling(time.Second).
				WithTimeout(time.Minute).
				Should(Succeed())
		})
	})

	When("port forwarding to a Pod", func() {
		It("should serve the nginx default page", func(ctx context.Context) {
			nginxPod, _ := virtualCluster.NewNginxPod("")

			By("Port forwarding to the nginx Pod")

			localPort := virtualCluster.PortForward(nginxPod, 80)
			url := fmt.Sprintf("http://127.0.0.1:%d", localPort)

			By("Checking the nginx default page is served through the port forward")

			// do multiple requests, since each one is forwarded with a different connection
			for range 3 {
				Eventually(func(g Gomega) {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
					g.Expect(err).To(Not(HaveOccurred()))

					resp, err := http.DefaultClient.Do(req)
					g.Expect(err).To(Not(HaveOccurred()))

					defer func() {
						g.Expect(resp.Body.Close()).To(Succeed())
					}()

					body, err := io.ReadAll(resp.Body)
					g.Expect(err).To(Not(HaveOccurred()))

					g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
					g.Expect(string(body)).To(ContainSubstring("Welcome to nginx!"))
				}).
					WithPolling(time.Second).
					WithTimeout(time.Second * 30).
					Should(Succeed())
			}
		})
	})
})
