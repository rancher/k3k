package upgrade_test

import (
	"context"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/api/v1/pod"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	clustercontroller "github.com/rancher/k3k/pkg/controller/cluster"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This is a smoke test for upgrading the k3k controller itself: it installs the
// latest released k3k, provisions a shared-mode and a virtual-mode cluster (both
// with dynamic persistence) running an nginx app, then upgrades k3k to the build
// from source and verifies that the clusters, and the workloads inside them, are
// unaffected, and that the new controller can still reconcile, create and delete
// clusters.
//
// It is deliberately mode-agnostic and does not assert on any single controller
// implementation detail: the signal is the reconciled Cluster status, the server
// pods, the reachability of the virtual API and the surviving workloads.
// This catches rancher/k3k#559 (the immutable server StatefulSet field renamed
// by PR #869 breaks reconciliation of dynamic-persistence clusters on upgrade)
// as well as any other upgrade regression.
//
// HCP mode is intentionally not covered: it was added after the latest stable
// release, so it cannot be provisioned by the version we upgrade from. It should
// be added here as soon as a stable release ships it.
//
// The spec mutates the shared k3k release in k3k-system, so it is Serial and
// runs in its own dedicated test suite.
var _ = When("k3k is upgraded from the latest released version", Ordered, Serial, func() {
	var (
		namespaceName  string
		clusterVirtual *v1beta1.Cluster
		clusterShared  *v1beta1.Cluster
		podUIDs        []types.UID
	)

	ctx := context.Background()

	BeforeAll(func() {
		// Guard early: REPO has to point at the images built from this checkout and available to every node
		// (in CI they are tagged `k3k.local/...` and imported into containerd directly).
		Expect(os.Getenv("REPO")).NotTo(BeEmpty(), "REPO must be set to the image repository of the build under test")

		By("Installing the latest released k3k")

		expectCmd(runCmd("helm", "repo", "add", "k3k", "https://rancher.github.io/k3k", "--force-update"))
		expectCmd(runCmd("helm", "repo", "update"))

		By("Cleaning up old k3k installation")

		stdout, stderr, err := runCmd("helm", "list", "-q", "-n", k3kNamespace)
		Expect(err).NotTo(HaveOccurred())

		if len(stdout+stderr) > 0 {
			expectCmd(runCmd("helm", "uninstall", "--namespace", k3kNamespace, "k3k"))
			expectCmd(runCmd("kubectl", "delete", "crd", "clusters.k3k.io", "virtualclusterpolicies.k3k.io", "--ignore-not-found"))
		}

		expectCmd(runCmd(
			"helm", "install",
			"--namespace", k3kNamespace, "--create-namespace",
			"--timeout", "5m",
			"--wait",
			"k3k", "k3k/k3k",
		))

		namespace := fwk3k.CreateNamespace(k8s)
		namespaceName = namespace.Name

		By("Creating a virtual-mode cluster with the released k3k")

		clusterVirtual = NewCluster(namespaceName, func(c *v1beta1.Cluster) {
			c.Spec.Mode = v1beta1.VirtualClusterMode
		})

		CreateCluster(ctx, clusterVirtual)

		By("Creating a shared-mode cluster with the released k3k")

		clusterShared = NewCluster(namespaceName, func(c *v1beta1.Cluster) {
			c.Spec.Mode = v1beta1.SharedClusterMode
			c.Spec.Persistence.Type = v1beta1.DynamicPersistenceMode
		})

		CreateCluster(ctx, clusterShared)

		By("Deploying an app in the shared-mode cluster")

		client := newVirtualK8sClient(ctx, clusterShared)
		deployApp(ctx, client)

		podUIDs = listAppPodUIDs(ctx, client)
		Expect(podUIDs).To(HaveLen(appReplicas))

		By("Upgrading k3k to the build from source")
		expectCmd(runCmd("make", "-C", "../..", "install"))

		By("Waiting for the new controller rollout to complete")
		expectCmd(runCmd("kubectl", "rollout", "status", "deployment/k3k",
			"--namespace", k3kNamespace, "--timeout", "3m",
		))

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespaceName)
		})
	})

	It("keeps the existing virtual-mode clusters healthy", func() {
		By("Verifying the virtual-mode cluster " + clusterVirtual.Name + " is healthy after the upgrade")
		assertClusterHealthy(ctx, clusterVirtual)
	})

	It("keeps the existing shared-mode clusters healthy", func() {
		By("Verifying the shared-mode cluster " + clusterShared.Name + " is healthy after the upgrade")
		assertClusterHealthy(ctx, clusterShared)

		By("Verifying the app in the shared-mode cluster " + clusterShared.Name + " is still available")
		client := newVirtualK8sClient(ctx, clusterShared)
		assertAppAvailable(ctx, client)

		// The upgrade replaces the controller (and, in shared mode, the virtual
		// kubelet), but it must never recreate the pods of a user workload.
		By("Verifying the app pods in the shared-mode cluster " + clusterShared.Name + " were not recreated")
		Expect(listAppPodUIDs(ctx, client)).To(ConsistOf(podUIDs))
	})

	It("can still create new clusters in virtual-mode", func() {
		clusterVirtual2 := NewCluster(namespaceName, func(c *v1beta1.Cluster) {
			c.Spec.Mode = v1beta1.VirtualClusterMode
		})

		CreateCluster(ctx, clusterVirtual2)

		By("Checking it's healthy")

		assertClusterHealthy(ctx, clusterVirtual2)
	})

	It("can still create new clusters in shared-mode", func() {
		clusterShared2 := NewCluster(namespaceName, func(c *v1beta1.Cluster) {
			c.Spec.Mode = v1beta1.SharedClusterMode
			c.Spec.Persistence.Type = v1beta1.DynamicPersistenceMode
		})

		CreateCluster(ctx, clusterShared2)

		By("Checking it's healthy")

		assertClusterHealthy(ctx, clusterShared2)
	})
})

// assertClusterHealthy verifies that a cluster is working:
// the Cluster reconciled successfully, its server pods are Ready, and the virtual API is reachable.
func assertClusterHealthy(ctx context.Context, cluster *v1beta1.Cluster) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		// 1. The Cluster reconciles successfully with the new controller. On the
		// immutable-field bug the reconcile error is written to the status, which
		// flips Phase to Failed and the Ready condition to False, so this is the
		// signal that catches the regression.
		var current v1beta1.Cluster
		g.Expect(k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(cluster), &current)).To(Succeed())
		g.Expect(current.Status.Phase).To(Equal(v1beta1.ClusterReady))

		cond := meta.FindStatusCondition(current.Status.Conditions, clustercontroller.ConditionReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))

		// 2. The server pods are Ready.
		serverPods := listServerPods(ctx, cluster)
		g.Expect(serverPods).NotTo(BeEmpty())

		for i := range serverPods {
			_, podCond := pod.GetPodCondition(&serverPods[i].Status, corev1.PodReady)
			g.Expect(podCond).NotTo(BeNil())
			g.Expect(podCond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}

		// 3. The virtual API is reachable.
		client := newVirtualK8sClient(ctx, cluster)

		_, err := client.Discovery().ServerVersion()
		g.Expect(err).NotTo(HaveOccurred())
	}).
		WithTimeout(time.Minute * 3).
		WithPolling(time.Second * 5).
		Should(Succeed())
}

// expectCmd runs a command via the framework helpers and fails the spec on
// error, surfacing stdout+stderr.
func expectCmd(stdout, stderr string, err error) {
	GinkgoHelper()

	Expect(err).NotTo(HaveOccurred(), stdout+stderr)
	GinkgoWriter.Println(stdout)
}
