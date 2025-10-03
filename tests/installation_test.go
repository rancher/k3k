package k3k_test

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("k3k is installed", Label("e2e"), func() {
	It("is in Running status", func() {
		// check that the controller is running
		Eventually(func() bool {
			opts := v1.ListOptions{LabelSelector: "app.kubernetes.io/name=k3k"}
			podList, err := k8s.CoreV1().Pods(k3kNamespace).List(context.Background(), opts)

			Expect(err).To(Not(HaveOccurred()))
			Expect(podList.Items).To(Not(BeEmpty()))

			for _, pod := range podList.Items {
				if pod.Status.Phase == corev1.PodRunning {
					return true
				}
			}
			return false
		}).
			WithTimeout(time.Second * 10).
			WithPolling(time.Second).
			Should(BeTrue())
	})
})
