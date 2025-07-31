package syncer_test

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/controller/syncer"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var PriorityClassTests = func() {
	var (
		namespace string
		cluster   v1alpha1.Cluster
	)

	BeforeEach(func() {
		ctx := context.Background()

		ns := v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"},
		}
		err := hostTestEnv.k8sClient.Create(ctx, &ns)
		Expect(err).NotTo(HaveOccurred())

		namespace = ns.Name

		cluster = v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cluster-",
				Namespace:    namespace,
			},
		}
		err = hostTestEnv.k8sClient.Create(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		err = syncer.AddPriorityClassReconciler(ctx, virtManager, hostManager, cluster.Name, cluster.Namespace)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		ns := v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := hostTestEnv.k8sClient.Delete(context.Background(), &ns)
		Expect(err).NotTo(HaveOccurred())
	})

	It("creates a priorityClass on the host cluster", func() {
		ctx := context.Background()

		priorityClass := &schedulingv1.PriorityClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "pc-",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Value: 1001,
		}

		err := virtTestEnv.k8sClient.Create(ctx, priorityClass)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created priorityClass %s in virtual cluster", priorityClass.Name))

		var hostPriorityClass schedulingv1.PriorityClass
		hostPriorityClassName := translateName(cluster, priorityClass.Namespace, priorityClass.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostPriorityClassName}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostPriorityClass)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created priorityClass %s in host cluster", hostPriorityClassName))

		Expect(hostPriorityClass.Value).To(Equal(priorityClass.Value))
		Expect(hostPriorityClass.Labels).To(ContainElement("bar"))

		GinkgoWriter.Printf("labels: %v\n", hostPriorityClass.Labels)
	})

	It("updates a priorityClass on the host cluster", func() {
		ctx := context.Background()

		priorityClass := &schedulingv1.PriorityClass{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "pc-"},
			Value:      1001,
		}

		err := virtTestEnv.k8sClient.Create(ctx, priorityClass)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created priorityClass %s in virtual cluster", priorityClass.Name))

		var hostPriorityClass schedulingv1.PriorityClass
		hostPriorityClassName := translateName(cluster, priorityClass.Namespace, priorityClass.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostPriorityClassName}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostPriorityClass)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created priorityClass %s in host cluster", hostPriorityClassName))

		Expect(hostPriorityClass.Value).To(Equal(priorityClass.Value))
		Expect(hostPriorityClass.Labels).NotTo(ContainElement("bar"))

		key := client.ObjectKeyFromObject(priorityClass)
		err = virtTestEnv.k8sClient.Get(ctx, key, priorityClass)
		Expect(err).NotTo(HaveOccurred())

		priorityClass.Labels = map[string]string{"foo": "bar"}

		// update virtual priorityClass
		err = virtTestEnv.k8sClient.Update(ctx, priorityClass)
		Expect(err).NotTo(HaveOccurred())
		Expect(priorityClass.Labels).To(ContainElement("bar"))

		// check hostPriorityClass
		Eventually(func() map[string]string {
			key := client.ObjectKey{Name: hostPriorityClassName}
			err = hostTestEnv.k8sClient.Get(ctx, key, &hostPriorityClass)
			Expect(err).NotTo(HaveOccurred())
			return hostPriorityClass.Labels
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(ContainElement("bar"))
	})

	It("deletes a priorityClass on the host cluster", func() {
		ctx := context.Background()

		priorityClass := &schedulingv1.PriorityClass{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "pc-"},
			Value:      1001,
		}

		err := virtTestEnv.k8sClient.Create(ctx, priorityClass)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created priorityClass %s in virtual cluster", priorityClass.Name))

		var hostPriorityClass schedulingv1.PriorityClass
		hostPriorityClassName := translateName(cluster, priorityClass.Namespace, priorityClass.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostPriorityClassName}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostPriorityClass)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created priorityClass %s in host cluster", hostPriorityClassName))

		Expect(hostPriorityClass.Value).To(Equal(priorityClass.Value))

		err = virtTestEnv.k8sClient.Delete(ctx, priorityClass)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			key := client.ObjectKey{Name: hostPriorityClassName}
			err := hostTestEnv.k8sClient.Get(ctx, key, &hostPriorityClass)
			return apierrors.IsNotFound(err)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeTrue())
	})

	It("creates a priorityClass on the host cluster with the globalDefault annotation", func() {
		ctx := context.Background()

		priorityClass := &schedulingv1.PriorityClass{
			ObjectMeta:    metav1.ObjectMeta{GenerateName: "pc-"},
			Value:         1001,
			GlobalDefault: true,
		}

		err := virtTestEnv.k8sClient.Create(ctx, priorityClass)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Created priorityClass %s in virtual cluster", priorityClass.Name))

		var hostPriorityClass schedulingv1.PriorityClass
		hostPriorityClassName := translateName(cluster, priorityClass.Namespace, priorityClass.Name)

		Eventually(func() error {
			key := client.ObjectKey{Name: hostPriorityClassName}
			return hostTestEnv.k8sClient.Get(ctx, key, &hostPriorityClass)
		}).
			WithPolling(time.Millisecond * 300).
			WithTimeout(time.Second * 10).
			Should(BeNil())

		By(fmt.Sprintf("Created priorityClass %s in host cluster without the GlobalDefault value", hostPriorityClassName))

		Expect(hostPriorityClass.Value).To(Equal(priorityClass.Value))
		Expect(hostPriorityClass.GlobalDefault).To(BeFalse())
		Expect(hostPriorityClass.Annotations[syncer.PriorityClassGlobalDefaultAnnotation]).To(Equal("true"))
	})
}
