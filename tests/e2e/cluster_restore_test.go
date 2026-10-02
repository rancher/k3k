package e2e_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/kubernetes/pkg/api/v1/pod"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k3sv1 "github.com/k3s-io/api/k3s.cattle.io/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	fwclient "github.com/rancher/k3k/tests/framework/client"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("restoring a single node shared mode cluster with local snapshot", Ordered, Label(restoreTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	var nginxPod *corev1.Pod

	BeforeAll(func() {
		ctx := GinkgoT().Context()

		virtualCluster = NewVirtualClusterWithType(v1beta1.DynamicPersistenceMode)
		scheme := fwclient.NewScheme()
		err := k3sv1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())

		virtualCluster.CtrlClient = NewVirtualCtrlClient(virtualCluster.RestConfig, scheme)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})

		// creating an nginx pod
		nginxPod, _ = virtualCluster.NewNginxPod(corev1.NamespaceDefault)

		// taking a snapshot
		localSnapshot := newSnapshot(virtualCluster.Cluster.Name, virtualCluster.Cluster.Namespace, "")

		// remove the nginx Pod
		err = virtualCluster.CtrlClient.Delete(ctx, nginxPod)
		Expect(err).To(Not(HaveOccurred()))

		// restore the snapshot
		newRestore(virtualCluster.Cluster.Namespace, virtualCluster.Cluster.Name, localSnapshot.Name)
	})
	It("will restore the cluster to ready state", func(ctx context.Context) {
		waitForCluster(ctx, virtualCluster.Cluster)
	})
	It("will restore the cluster with previous Nginx pod", func(ctx context.Context) {
		Consistently(func(g Gomega) {
			err := virtualCluster.CtrlClient.Get(ctx, client.ObjectKeyFromObject(nginxPod), nginxPod)
			g.Expect(err).To(Not(HaveOccurred()))

			// make sure that pod is ready
			_, cond := pod.GetPodCondition(&nginxPod.Status, corev1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(corev1.ConditionTrue))
		}).
			WithTimeout(time.Second * 30).
			WithPolling(time.Second * 2).
			Should(Succeed())
	})
	It("can create new nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})
})

var _ = When("restoring a HA node shared mode cluster with local snapshot", Ordered, Label(restoreTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	var nginxPod *corev1.Pod

	BeforeAll(func() {
		ctx := GinkgoT().Context()

		virtualCluster = NewVirtualClusterWithOpts(func(c *v1beta1.Cluster) {
			c.Spec.Persistence.Type = v1beta1.DynamicPersistenceMode
			c.Spec.Servers = new(int32(3))
		})

		scheme := fwclient.NewScheme()
		err := k3sv1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())

		virtualCluster.CtrlClient = NewVirtualCtrlClient(virtualCluster.RestConfig, scheme)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})

		// creating an nginx pod
		nginxPod, _ = virtualCluster.NewNginxPod(corev1.NamespaceDefault)

		// taking a snapshot
		localSnapshot := newSnapshot(virtualCluster.Cluster.Name, virtualCluster.Cluster.Namespace, "")

		// remove the nginx Pod
		err = virtualCluster.CtrlClient.Delete(ctx, nginxPod)
		Expect(err).To(Not(HaveOccurred()))

		// restore the snapshot
		newRestore(virtualCluster.Cluster.Namespace, virtualCluster.Cluster.Name, localSnapshot.Name)
	})
	It("will restore the cluster to ready state", func(ctx context.Context) {
		waitForCluster(ctx, virtualCluster.Cluster)
	})
	It("will restore the cluster with previous Nginx pod", func(ctx context.Context) {
		Consistently(func(g Gomega) {
			err := virtualCluster.CtrlClient.Get(ctx, client.ObjectKeyFromObject(nginxPod), nginxPod)
			g.Expect(err).To(Not(HaveOccurred()))

			// make sure that pod is ready
			_, cond := pod.GetPodCondition(&nginxPod.Status, corev1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(corev1.ConditionTrue))
		}).
			WithTimeout(time.Second * 30).
			WithPolling(time.Second * 2).
			Should(Succeed())
	})
	It("can create new nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})
})

var _ = When("restoring a single node shared mode cluster with s3 snapshot", Ordered, Label(restoreTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	var nginxPod *corev1.Pod

	BeforeAll(func() {
		ctx := GinkgoT().Context()

		virtualCluster = NewVirtualClusterWithType(v1beta1.DynamicPersistenceMode)
		scheme := fwclient.NewScheme()
		err := k3sv1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())

		virtualCluster.CtrlClient = NewVirtualCtrlClient(virtualCluster.RestConfig, scheme)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})

		// creating an nginx pod
		nginxPod, _ = virtualCluster.NewNginxPod(corev1.NamespaceDefault)

		deployS3MockInCluster(virtualCluster.Cluster.Namespace)

		endpoint := fmt.Sprintf("s3-mock.%s.svc:%d", virtualCluster.Cluster.Namespace, s3MockPort)

		secret := newS3ConfigSecret(s3ConfigSecretName, virtualCluster.Cluster.Namespace, endpoint)
		err = k8sClient.Create(ctx, secret)
		Expect(err).ToNot(HaveOccurred())

		// taking a snapshot
		localSnapshot := newSnapshot(virtualCluster.Cluster.Name, virtualCluster.Cluster.Namespace, s3ConfigSecretName)

		// remove the nginx Pod
		err = virtualCluster.CtrlClient.Delete(ctx, nginxPod)
		Expect(err).To(Not(HaveOccurred()))

		// restore the snapshot
		newRestore(virtualCluster.Cluster.Namespace, virtualCluster.Cluster.Name, localSnapshot.Name)
	})
	It("will restore the cluster to ready state", func(ctx context.Context) {
		waitForCluster(ctx, virtualCluster.Cluster)
	})
	It("will restore the cluster with previous Nginx pod", func(ctx context.Context) {
		Consistently(func(g Gomega) {
			err := virtualCluster.CtrlClient.Get(ctx, client.ObjectKeyFromObject(nginxPod), nginxPod)
			g.Expect(err).To(Not(HaveOccurred()))

			// make sure that pod is ready
			_, cond := pod.GetPodCondition(&nginxPod.Status, corev1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(corev1.ConditionTrue))
		}).
			WithTimeout(time.Second * 30).
			WithPolling(time.Second * 2).
			Should(Succeed())
	})
	It("can create new nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})
})

var _ = When("restoring a HA node shared mode cluster with s3 snapshot", Ordered, Label(restoreTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	var nginxPod *corev1.Pod

	BeforeAll(func() {
		ctx := GinkgoT().Context()

		virtualCluster = NewVirtualClusterWithOpts(func(c *v1beta1.Cluster) {
			c.Spec.Persistence.Type = v1beta1.DynamicPersistenceMode
			c.Spec.Servers = new(int32(3))
		})

		scheme := fwclient.NewScheme()
		err := k3sv1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())

		virtualCluster.CtrlClient = NewVirtualCtrlClient(virtualCluster.RestConfig, scheme)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})

		// creating an nginx pod
		nginxPod, _ = virtualCluster.NewNginxPod(corev1.NamespaceDefault)

		deployS3MockInCluster(virtualCluster.Cluster.Namespace)

		endpoint := fmt.Sprintf("s3-mock.%s.svc:%d", virtualCluster.Cluster.Namespace, s3MockPort)

		secret := newS3ConfigSecret(s3ConfigSecretName, virtualCluster.Cluster.Namespace, endpoint)
		err = k8sClient.Create(ctx, secret)
		Expect(err).ToNot(HaveOccurred())

		// taking a snapshot
		localSnapshot := newSnapshot(virtualCluster.Cluster.Name, virtualCluster.Cluster.Namespace, s3ConfigSecretName)

		// remove the nginx Pod
		err = virtualCluster.CtrlClient.Delete(ctx, nginxPod)
		Expect(err).To(Not(HaveOccurred()))

		// restore the snapshot
		newRestore(virtualCluster.Cluster.Namespace, virtualCluster.Cluster.Name, localSnapshot.Name)
	})
	It("will restore the cluster to ready state", func(ctx context.Context) {
		waitForCluster(ctx, virtualCluster.Cluster)
	})
	It("will restore the cluster with previous Nginx pod", func(ctx context.Context) {
		Consistently(func(g Gomega) {
			err := virtualCluster.CtrlClient.Get(ctx, client.ObjectKeyFromObject(nginxPod), nginxPod)
			g.Expect(err).To(Not(HaveOccurred()))

			// make sure that pod is ready
			_, cond := pod.GetPodCondition(&nginxPod.Status, corev1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(corev1.ConditionTrue))
		}).
			WithTimeout(time.Second * 30).
			WithPolling(time.Second * 2).
			Should(Succeed())
	})
	It("can create new nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})
})
