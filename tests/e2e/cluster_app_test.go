package k3k_test

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"

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
			DeleteNamespaces(virtualCluster.Cluster.Namespace)
		})
	})

	When("creating a Deployment with a PVC", func() {
		var (
			deployment *appsv1.Deployment
			pvc        *v1.PersistentVolumeClaim

			labels = map[string]string{
				"app": "k3k-deployment-test-app",
			}
		)

		BeforeEach(func() {
			var err error

			ctx := context.Background()

			namespace := NewNamespace()

			By("Creating the PVC")

			pvc = &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "k3k-test-app-",
					Namespace:    namespace.Name,
				},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
					Resources: v1.VolumeResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}

			pvc, err = virtualCluster.Client.CoreV1().PersistentVolumeClaims("default").Create(ctx, pvc, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			By("Creating the Deployment")

			deployment = &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "k3k-test-app-",
					Namespace:    namespace.Name,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
					Selector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					Template: v1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  "nginx",
									Image: "nginx",
									VolumeMounts: []v1.VolumeMount{{
										Name:      "data-volume",
										MountPath: "/data",
									}},
								},
							},
							Volumes: []v1.Volume{{
								Name: "data-volume",
								VolumeSource: v1.VolumeSource{
									PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
										ClaimName: pvc.Name,
									},
								},
							}},
						},
					},
				},
			}

			deployment, err = virtualCluster.Client.AppsV1().Deployments(namespace.Name).Create(ctx, deployment, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			DeferCleanup(func() {
				err := virtualCluster.Client.AppsV1().Deployments(namespace.Name).Delete(ctx, deployment.Name, metav1.DeleteOptions{})
				Expect(err).To(Not(HaveOccurred()))

				err = virtualCluster.Client.CoreV1().PersistentVolumeClaims(namespace.Name).Delete(ctx, pvc.Name, metav1.DeleteOptions{})
				Expect(err).To(Not(HaveOccurred()))

				DeleteNamespaces(namespace.Name)
			})
		})

		It("should bound the PVC in the virtual cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				virtualPVC, err := virtualCluster.Client.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(ctx, pvc.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(virtualPVC.Status.Phase).To(Equal(v1.ClaimBound))
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
				g.Expect(hostPVC.Status.Phase).To(Equal(v1.ClaimBound))
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

				pods, err := virtualCluster.Client.CoreV1().Pods(deployment.Namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).Should(HaveLen(int(*deployment.Spec.Replicas)))

				for _, pod := range pods.Items {
					g.Expect(pod.Status.Phase).To(Equal(v1.PodRunning))
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

				pods, err := virtualCluster.Client.CoreV1().Pods(deployment.Namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).Should(HaveLen(int(*deployment.Spec.Replicas)))

				for _, pod := range pods.Items {
					hostPodName := translator.NamespacedName(&pod)

					pod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(pod.Status.Phase).To(Equal(v1.PodRunning))
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

			labels = map[string]string{
				"app": "k3k-sts-test-app",
			}
		)

		BeforeEach(func() {
			var err error

			ctx := context.Background()

			namespace := NewNamespace()

			By("Creating the StatefulSet")

			statefulSet = &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "k3k-sts-test-app-",
					Namespace:    namespace.Name,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
					Selector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					Template: v1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  "nginx",
									Image: "nginx",
									VolumeMounts: []v1.VolumeMount{{
										Name:      "www",
										MountPath: "/usr/share/nginx/html",
									}},
								},
							},
						},
					},
					VolumeClaimTemplates: []v1.PersistentVolumeClaim{{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: v1.PersistentVolumeClaimSpec{
							AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
							Resources: v1.VolumeResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceStorage: resource.MustParse("1Gi"),
								},
							},
						},
					}},
				},
			}

			statefulSet, err = virtualCluster.Client.AppsV1().StatefulSets("default").Create(ctx, statefulSet, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			DeferCleanup(func() {
				err := virtualCluster.Client.AppsV1().StatefulSets(namespace.Name).Delete(ctx, statefulSet.Name, metav1.DeleteOptions{})
				Expect(err).To(Not(HaveOccurred()))

				DeleteNamespaces(namespace.Name)
			})
		})

		It("should bound the PVCs in the virtual cluster", func() {
			ctx := context.Background()

			Eventually(func(g Gomega) {
				labelSelector := metav1.FormatLabelSelector(statefulSet.Spec.Selector)
				listOpts := metav1.ListOptions{LabelSelector: labelSelector}

				pvcs, err := virtualCluster.Client.CoreV1().PersistentVolumeClaims(statefulSet.Namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())

				for _, pvc := range pvcs.Items {
					g.Expect(pvc.Status.Phase).To(Equal(v1.ClaimBound))
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

				pvcs, err := virtualCluster.Client.CoreV1().PersistentVolumeClaims(statefulSet.Namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())

				for _, pvc := range pvcs.Items {
					hostPVCName := translator.NamespacedName(&pvc)

					hostPVC, err := k8s.CoreV1().PersistentVolumeClaims(hostPVCName.Namespace).Get(ctx, hostPVCName.Name, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(hostPVC.Status.Phase).To(Equal(v1.ClaimBound))
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

				pods, err := virtualCluster.Client.CoreV1().Pods(statefulSet.Namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).Should(HaveLen(int(*statefulSet.Spec.Replicas)))

				for _, pod := range pods.Items {
					g.Expect(pod.Status.Phase).To(Equal(v1.PodRunning))
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

				pods, err := virtualCluster.Client.CoreV1().Pods(statefulSet.Namespace).List(ctx, listOpts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).Should(HaveLen(int(*statefulSet.Spec.Replicas)))

				for _, pod := range pods.Items {
					hostPodName := translator.NamespacedName(&pod)

					pod, err := k8s.CoreV1().Pods(hostPodName.Namespace).Get(ctx, hostPodName.Name, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(pod.Status.Phase).To(Equal(v1.PodRunning))
				}
			}).
				WithPolling(time.Second * 3).
				WithTimeout(time.Minute * 3).
				Should(Succeed())
		})
	})
})
