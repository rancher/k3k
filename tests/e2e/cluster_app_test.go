package k3k_test

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("In a shared cluster", Label(e2eTestLabel), Ordered, func() {
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

	When("creating a Deployment with a PVC", func() {
		var (
			deployment *appsv1.Deployment
			pvc        *corev1.PersistentVolumeClaim

			namespace = "default"
		)

		BeforeEach(func() {
			var err error

			ctx := context.Background()

			labels := map[string]string{
				"app": "k3k-deployment-test-app-" + rand.String(5),
			}

			By("Creating the PVC")

			pvc = &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "k3k-test-app-",
					Namespace:    namespace,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}

			pvc, err = virtualCluster.Client.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			DeferCleanup(func() {
				err := virtualCluster.Client.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{})
				Expect(err).To(Not(HaveOccurred()))
			})

			By("Creating the Deployment")

			deployment = &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "k3k-test-app-",
					Namespace:    namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
					Selector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "nginx",
									Image: "nginx",
									VolumeMounts: []corev1.VolumeMount{{
										Name:      "data-volume",
										MountPath: "/data",
									}},
								},
							},
							Volumes: []corev1.Volume{{
								Name: "data-volume",
								VolumeSource: corev1.VolumeSource{
									PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
										ClaimName: pvc.Name,
									},
								},
							}},
						},
					},
				},
			}

			deployment, err = virtualCluster.Client.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			DeferCleanup(func() {
				err := virtualCluster.Client.AppsV1().Deployments(namespace).Delete(ctx, deployment.Name, metav1.DeleteOptions{})
				Expect(err).To(Not(HaveOccurred()))
			})
		})

		It("should bound the PVC in the virtual cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				virtualPVC, err := virtualCluster.Client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvc.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(virtualPVC.Status.Phase).To(Equal(corev1.ClaimBound))
			}).
				WithPolling(time.Second * 3).
				WithTimeout(time.Minute * 3).
				Should(Succeed())
		})

		It("should bound the PVC in the host cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				hostPVCName := translator.NamespacedName(pvc)

				hostPVC, err := k8s.CoreV1().PersistentVolumeClaims(hostPVCName.Namespace).Get(ctx, hostPVCName.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostPVC.Status.Phase).To(Equal(corev1.ClaimBound))
			}).
				WithPolling(time.Second * 3).
				WithTimeout(time.Minute * 3).
				Should(Succeed())
		})

		It("should have the Pods running in the virtual cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				labelSelector := metav1.FormatLabelSelector(deployment.Spec.Selector)
				listOpts := metav1.ListOptions{LabelSelector: labelSelector}

				pods, err := virtualCluster.Client.CoreV1().Pods(namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).Should(HaveLen(int(*deployment.Spec.Replicas)))

				for _, pod := range pods.Items {
					g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
				}
			}).
				WithPolling(time.Second * 3).
				WithTimeout(time.Minute * 3).
				Should(Succeed())
		})

		It("should have the Pods running in the host cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				labelSelector := metav1.FormatLabelSelector(deployment.Spec.Selector)
				listOpts := metav1.ListOptions{LabelSelector: labelSelector}

				pods, err := virtualCluster.Client.CoreV1().Pods(namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).Should(HaveLen(int(*deployment.Spec.Replicas)))

				for _, pod := range pods.Items {
					hostPodName := translator.NamespacedName(&pod)

					pod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
				}
			}).
				WithPolling(time.Second * 3).
				WithTimeout(time.Minute * 3).
				Should(Succeed())
		})
	})

	When("creating a StatefulSet with a PVC", func() {
		var (
			statefulSet *appsv1.StatefulSet

			namespace = "default"
		)

		BeforeEach(func() {
			var err error

			ctx := context.Background()

			By("Creating the StatefulSet")

			labels := map[string]string{
				"app": "k3k-sts-test-app-" + rand.String(5),
			}

			statefulSet = &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "k3k-sts-test-app-",
					Namespace:    namespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
					Selector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "nginx",
									Image: "nginx",
									VolumeMounts: []corev1.VolumeMount{{
										Name:      "www",
										MountPath: "/usr/share/nginx/html",
									}},
								},
							},
						},
					},
					VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "www",
							Labels: labels,
						},
						Spec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceStorage: resource.MustParse("1Gi"),
								},
							},
						},
					}},
				},
			}

			statefulSet, err = virtualCluster.Client.AppsV1().StatefulSets(namespace).Create(ctx, statefulSet, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			DeferCleanup(func() {
				err := virtualCluster.Client.AppsV1().StatefulSets(namespace).Delete(ctx, statefulSet.Name, metav1.DeleteOptions{})
				Expect(err).To(Not(HaveOccurred()))
			})
		})

		It("should bound the PVCs in the virtual cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				labelSelector := metav1.FormatLabelSelector(statefulSet.Spec.Selector)
				listOpts := metav1.ListOptions{LabelSelector: labelSelector}

				pvcs, err := virtualCluster.Client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())

				for _, pvc := range pvcs.Items {
					g.Expect(pvc.Status.Phase).To(Equal(corev1.ClaimBound))
				}
			}).
				WithPolling(time.Second * 3).
				WithTimeout(time.Minute * 3).
				Should(Succeed())
		})

		It("should bound the PVCs in the host cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				labelSelector := metav1.FormatLabelSelector(statefulSet.Spec.Selector)
				listOpts := metav1.ListOptions{LabelSelector: labelSelector}

				pvcs, err := virtualCluster.Client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())

				for _, pvc := range pvcs.Items {
					hostPVCName := translator.NamespacedName(&pvc)

					hostPVC, err := k8s.CoreV1().PersistentVolumeClaims(hostPVCName.Namespace).Get(ctx, hostPVCName.Name, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(hostPVC.Status.Phase).To(Equal(corev1.ClaimBound))
				}
			}).
				WithPolling(time.Second * 3).
				WithTimeout(time.Minute * 3).
				Should(Succeed())
		})

		It("should have the Pods running in the virtual cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				labelSelector := metav1.FormatLabelSelector(statefulSet.Spec.Selector)
				listOpts := metav1.ListOptions{LabelSelector: labelSelector}

				pods, err := virtualCluster.Client.CoreV1().Pods(namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).Should(HaveLen(int(*statefulSet.Spec.Replicas)))

				for _, pod := range pods.Items {
					g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
				}
			}).
				WithPolling(time.Second * 3).
				WithTimeout(time.Minute * 3).
				Should(Succeed())
		})

		It("should have the Pods running in the host cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				labelSelector := metav1.FormatLabelSelector(statefulSet.Spec.Selector)
				listOpts := metav1.ListOptions{LabelSelector: labelSelector}

				pods, err := virtualCluster.Client.CoreV1().Pods(namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).Should(HaveLen(int(*statefulSet.Spec.Replicas)))

				for _, pod := range pods.Items {
					hostPodName := translator.NamespacedName(&pod)

					pod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
				}
			}).
				WithPolling(time.Second * 3).
				WithTimeout(time.Minute * 3).
				Should(Succeed())
		})
	})
})
