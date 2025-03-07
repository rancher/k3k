package k3k_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("two virtual clusters are installed", Label("e2e"), func() {

	var (
		cluster1 *VirtualCluster
		cluster2 *VirtualCluster
	)

	BeforeEach(func() {
		clusters := NewVirtualClusters(2)
		cluster1 = clusters[0]
		cluster2 = clusters[1]
	})

	AfterEach(func() {
		DeleteNamespaces(cluster1.Cluster.Namespace, cluster2.Cluster.Namespace)
	})

	It("can create pods in each of them that are isolated", func() {

		pod1Cluster1, pod1Cluster1IP := cluster1.NewNginxPod("")
		pod2Cluster1, pod2Cluster1IP := cluster1.NewNginxPod("")
		pod1Cluster2, pod1Cluster2IP := cluster2.NewNginxPod("")

		var (
			stdout  string
			stderr  string
			curlCmd string
			err     error
		)

		By("Checking that Pods can reach themselves")

		curlCmd = "curl --no-progress-meter " + pod1Cluster1IP
		stdout, _, err = cluster1.ExecCmd(pod1Cluster1, curlCmd)
		Expect(err).To(Not(HaveOccurred()))
		Expect(stdout).To(ContainSubstring("Welcome to nginx!"))

		curlCmd = "curl --no-progress-meter " + pod2Cluster1IP
		stdout, _, err = cluster1.ExecCmd(pod2Cluster1, curlCmd)
		Expect(err).To(Not(HaveOccurred()))
		Expect(stdout).To(ContainSubstring("Welcome to nginx!"))

		curlCmd = "curl --no-progress-meter " + pod1Cluster2IP
		stdout, _, err = cluster2.ExecCmd(pod1Cluster2, curlCmd)
		Expect(err).To(Not(HaveOccurred()))
		Expect(stdout).To(ContainSubstring("Welcome to nginx!"))

		// Pods in the same Virtual Cluster should be able to reach each other
		// Pod1 should be able to call Pod2, and viceversa

		By("Checking that Pods in the same virtual clusters can reach each other")

		curlCmd = "curl --no-progress-meter " + pod2Cluster1IP
		stdout, _, err = cluster1.ExecCmd(pod1Cluster1, curlCmd)
		Expect(err).To(Not(HaveOccurred()))
		Expect(stdout).To(ContainSubstring("Welcome to nginx!"))

		curlCmd = "curl --no-progress-meter " + pod1Cluster1IP
		stdout, _, err = cluster1.ExecCmd(pod2Cluster1, curlCmd)
		Expect(err).To(Not(HaveOccurred()))
		Expect(stdout).To(ContainSubstring("Welcome to nginx!"))

		By("Checking that Pods in the different virtual clusters cannot reach each other")

		// Pods in Cluster 1 should not be able to reach the Pod in Cluster 2

		curlCmd = "curl --no-progress-meter " + pod1Cluster2IP
		_, stderr, err = cluster1.ExecCmd(pod1Cluster1, curlCmd)
		Expect(err).Should(HaveOccurred())
		Expect(stderr).To(ContainSubstring("Failed to connect"))

		curlCmd = "curl --no-progress-meter " + pod1Cluster2IP
		_, stderr, err = cluster1.ExecCmd(pod2Cluster1, curlCmd)
		Expect(err).To(HaveOccurred())
		Expect(stderr).To(ContainSubstring("Failed to connect"))

		// Pod in Cluster 2 should not be able to reach Pods in Cluster 1

		curlCmd = "curl --no-progress-meter " + pod1Cluster1IP
		_, stderr, err = cluster2.ExecCmd(pod1Cluster2, curlCmd)
		Expect(err).To(HaveOccurred())
		Expect(stderr).To(ContainSubstring("Failed to connect"))

		curlCmd = "curl --no-progress-meter " + pod2Cluster1IP
		_, stderr, err = cluster2.ExecCmd(pod1Cluster2, curlCmd)
		Expect(err).To(HaveOccurred())
		Expect(stderr).To(ContainSubstring("Failed to connect"))
	})
})
