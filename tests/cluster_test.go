package k3k_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

var _ = When("a cluster is installed", func() {

	var namespace string

	BeforeEach(func() {
		createdNS := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{GenerateName: "ns-"}}
		createdNS, err := k8s.CoreV1().Namespaces().Create(context.Background(), createdNS, v1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))
		namespace = createdNS.Name
	})

	It("will be created in shared mode", func() {
		cluster := v1alpha1.Cluster{
			ObjectMeta: v1.ObjectMeta{
				Name:      "mycluster",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				Mode:    v1alpha1.SharedClusterMode,
				Servers: pointer.Int32(1),
				Agents:  pointer.Int32(0),
			},
		}

		err := k8sClient.Create(context.Background(), &cluster)
		Expect(err).To(Not(HaveOccurred()))

		fmt.Fprintf(GinkgoWriter, "### cluster ###\n%+v\n", cluster)

		// check that at least the server Pod is in Ready state
		Eventually(func() bool {
			podList, err := k8s.CoreV1().Pods(namespace).List(context.Background(), v1.ListOptions{})
			Expect(err).To(Not(HaveOccurred()))

			isReady := false

		outer:
			for _, pod := range podList.Items {

				for _, condition := range pod.Status.Conditions {
					if condition.Status != corev1.ConditionTrue {
						continue
					}

					fmt.Fprintf(GinkgoWriter, "### pod: %s %+v\n", pod.Name, condition.Type)

					if condition.Type == corev1.PodReady {
						isReady = true
						break outer
					}
				}
			}

			return isReady
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeTrue())
	})
})
