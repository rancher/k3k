package k3k_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = When("k3k is installed", func() {
	It("is in Running status", func() {

		// check that the controller is running
		Eventually(func() bool {
			opts := v1.ListOptions{LabelSelector: "app.kubernetes.io/name=k3k"}
			podList, err := k8s.CoreV1().Pods("k3k-system").List(context.Background(), opts)

			Expect(err).To(Not(HaveOccurred()))
			Expect(podList.Items).To(Not(BeEmpty()))

			var isRunning bool
			for _, pod := range podList.Items {
				if pod.Status.Phase == corev1.PodRunning {
					isRunning = true
					break
				}
			}

			return isRunning
		}).
			WithTimeout(time.Second * 10).
			WithPolling(time.Second).
			Should(BeTrue())
	})
})

var _ = When("a ephemeral cluster is installed", func() {

	var namespace string

	BeforeEach(func() {
		createdNS := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{GenerateName: "ns-"}}
		createdNS, err := k8s.CoreV1().Namespaces().Create(context.Background(), createdNS, v1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))
		namespace = createdNS.Name
	})

	It("can create a nginx pod", func() {
		ctx := context.Background()

		cluster := v1alpha1.Cluster{
			ObjectMeta: v1.ObjectMeta{
				Name:      "mycluster",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				TLSSANs: []string{hostIP},
				Expose: &v1alpha1.ExposeConfig{
					NodePort: &v1alpha1.NodePortConfig{},
				},
				Persistence: v1alpha1.PersistenceConfig{
					Type: v1alpha1.EphemeralNodeType,
				},
			},
		}

		By(fmt.Sprintf("Creating virtual cluster %s/%s", cluster.Namespace, cluster.Name))
		NewVirtualCluster(cluster)

		By("Waiting to get a kubernetes client for the virtual cluster")
		virtualK8sClient := NewVirtualK8sClient(cluster)

		nginxPod := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "nginx",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "nginx",
					Image: "nginx",
				}},
			},
		}
		nginxPod, err := virtualK8sClient.CoreV1().Pods(nginxPod.Namespace).Create(ctx, nginxPod, v1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// check that the nginx Pod is up and running in the host cluster
		Eventually(func() bool {
			//labelSelector := fmt.Sprintf("%s=%s", translate.ClusterNameLabel, cluster.Namespace)
			podList, err := k8s.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{})
			Expect(err).To(Not(HaveOccurred()))

			for _, pod := range podList.Items {
				resourceName := pod.Annotations[translate.ResourceNameAnnotation]
				resourceNamespace := pod.Annotations[translate.ResourceNamespaceAnnotation]

				if resourceName == nginxPod.Name && resourceNamespace == nginxPod.Namespace {
					fmt.Fprintf(GinkgoWriter,
						"pod=%s resource=%s/%s status=%s\n",
						pod.Name, resourceNamespace, resourceName, pod.Status.Phase,
					)

					return pod.Status.Phase == corev1.PodRunning
				}
			}

			return false
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeTrue())
	})

	It("regenerates the bootstrap secret after a restart", func() {
		ctx := context.Background()

		cluster := v1alpha1.Cluster{
			ObjectMeta: v1.ObjectMeta{
				Name:      "mycluster",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				TLSSANs: []string{hostIP},
				Expose: &v1alpha1.ExposeConfig{
					NodePort: &v1alpha1.NodePortConfig{},
				},
				Persistence: v1alpha1.PersistenceConfig{
					Type: v1alpha1.EphemeralNodeType,
				},
			},
		}

		By(fmt.Sprintf("Creating virtual cluster %s/%s", cluster.Namespace, cluster.Name))
		NewVirtualCluster(cluster)

		By("Waiting to get a kubernetes client for the virtual cluster")
		virtualK8sClient := NewVirtualK8sClient(cluster)

		_, err := virtualK8sClient.DiscoveryClient.ServerVersion()
		Expect(err).To(Not(HaveOccurred()))

		labelSelector := "cluster=" + cluster.Name + ",role=server"
		serverPods, err := k8s.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))

		Expect(len(serverPods.Items)).To(Equal(1))
		serverPod := serverPods.Items[0]

		fmt.Fprintf(GinkgoWriter, "deleting pod %s/%s\n", serverPod.Namespace, serverPod.Name)
		err = k8s.CoreV1().Pods(namespace).Delete(ctx, serverPod.Name, v1.DeleteOptions{})
		Expect(err).To(Not(HaveOccurred()))

		By("Deleting server pod")

		// check that the server pods restarted
		Eventually(func() any {
			serverPods, err = k8s.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
			Expect(err).To(Not(HaveOccurred()))
			Expect(len(serverPods.Items)).To(Equal(1))
			return serverPods.Items[0].DeletionTimestamp
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeNil())

		By("Server pod up and running again")

		By("Using old k8s client configuration should fail")

		Eventually(func() bool {
			_, err = virtualK8sClient.DiscoveryClient.ServerVersion()
			var unknownAuthorityErr x509.UnknownAuthorityError
			return errors.As(err, &unknownAuthorityErr)
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeTrue())

		By("Recover new config should succeed")

		Eventually(func() error {
			virtualK8sClient = NewVirtualK8sClient(cluster)
			_, err = virtualK8sClient.DiscoveryClient.ServerVersion()
			return err
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeNil())
	})
})

var _ = When("a dynamic cluster is installed", func() {

	var namespace string

	BeforeEach(func() {
		createdNS := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{GenerateName: "ns-"}}
		createdNS, err := k8s.CoreV1().Namespaces().Create(context.Background(), createdNS, v1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))
		namespace = createdNS.Name
	})

	It("can create a nginx pod", func() {
		ctx := context.Background()

		cluster := v1alpha1.Cluster{
			ObjectMeta: v1.ObjectMeta{
				Name:      "mycluster",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				TLSSANs: []string{hostIP},
				Expose: &v1alpha1.ExposeConfig{
					NodePort: &v1alpha1.NodePortConfig{},
				},
				Persistence: v1alpha1.PersistenceConfig{
					Type: v1alpha1.DynamicNodesType,
				},
			},
		}

		By(fmt.Sprintf("Creating virtual cluster %s/%s", cluster.Namespace, cluster.Name))
		NewVirtualCluster(cluster)

		By("Waiting to get a kubernetes client for the virtual cluster")
		virtualK8sClient := NewVirtualK8sClient(cluster)

		nginxPod := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "nginx",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "nginx",
					Image: "nginx",
				}},
			},
		}
		nginxPod, err := virtualK8sClient.CoreV1().Pods(nginxPod.Namespace).Create(ctx, nginxPod, v1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// check that the nginx Pod is up and running in the host cluster
		Eventually(func() bool {
			//labelSelector := fmt.Sprintf("%s=%s", translate.ClusterNameLabel, cluster.Namespace)
			podList, err := k8s.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{})
			Expect(err).To(Not(HaveOccurred()))

			for _, pod := range podList.Items {
				resourceName := pod.Annotations[translate.ResourceNameAnnotation]
				resourceNamespace := pod.Annotations[translate.ResourceNamespaceAnnotation]

				if resourceName == nginxPod.Name && resourceNamespace == nginxPod.Namespace {
					fmt.Fprintf(GinkgoWriter,
						"pod=%s resource=%s/%s status=%s\n",
						pod.Name, resourceNamespace, resourceName, pod.Status.Phase,
					)

					return pod.Status.Phase == corev1.PodRunning
				}
			}

			return false
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeTrue())
	})

	It("regenerates the bootstrap secret after a restart", func() {
		ctx := context.Background()

		cluster := v1alpha1.Cluster{
			ObjectMeta: v1.ObjectMeta{
				Name:      "mycluster",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				TLSSANs: []string{hostIP},
				Expose: &v1alpha1.ExposeConfig{
					NodePort: &v1alpha1.NodePortConfig{},
				},
				Persistence: v1alpha1.PersistenceConfig{
					Type: v1alpha1.DynamicNodesType,
				},
			},
		}

		By(fmt.Sprintf("Creating virtual cluster %s/%s", cluster.Namespace, cluster.Name))
		NewVirtualCluster(cluster)

		By("Waiting to get a kubernetes client for the virtual cluster")
		virtualK8sClient := NewVirtualK8sClient(cluster)

		_, err := virtualK8sClient.DiscoveryClient.ServerVersion()
		Expect(err).To(Not(HaveOccurred()))

		labelSelector := "cluster=" + cluster.Name + ",role=server"
		serverPods, err := k8s.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))

		Expect(len(serverPods.Items)).To(Equal(1))
		serverPod := serverPods.Items[0]

		fmt.Fprintf(GinkgoWriter, "deleting pod %s/%s\n", serverPod.Namespace, serverPod.Name)
		err = k8s.CoreV1().Pods(namespace).Delete(ctx, serverPod.Name, v1.DeleteOptions{})
		Expect(err).To(Not(HaveOccurred()))

		By("Deleting server pod")

		// check that the server pods restarted
		Eventually(func() any {
			serverPods, err = k8s.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
			Expect(err).To(Not(HaveOccurred()))
			Expect(len(serverPods.Items)).To(Equal(1))
			return serverPods.Items[0].DeletionTimestamp
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeNil())

		By("Server pod up and running again")

		By("Using old k8s client configuration should succeed")

		_, err = virtualK8sClient.DiscoveryClient.ServerVersion()
		Expect(err).To(BeNil())

		Consistently(func() error {
			virtualK8sClient = NewVirtualK8sClient(cluster)
			_, err = virtualK8sClient.DiscoveryClient.ServerVersion()
			return err
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeNil())
	})
})
