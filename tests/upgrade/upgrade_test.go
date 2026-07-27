package upgrade_test

import (
	"context"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	clustercontroller "github.com/rancher/k3k/pkg/controller/cluster"
	fwcmd "github.com/rancher/k3k/tests/framework/cmd"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// repoRoot is the k3k repository root relative to the tests/upgrade package dir,
// used to run `make install` (which installs the build from source).
const repoRoot = "../.."

// upgradeCluster bundles a virtual cluster created before the upgrade.
type upgradeCluster struct {
	virtual *VirtualCluster
}

// This is a generic smoke test for upgrading the k3k controller itself: it
// installs the latest released k3k, provisions a shared-mode and a virtual-mode
// cluster (both with dynamic persistence), then upgrades k3k to the build from
// source and verifies every cluster is still alive and reconciling successfully.
//
// It is deliberately mode-agnostic and does not assert on any single controller
// implementation detail: the signal is the reconciled Cluster status, the server
// pods, the reachability of the virtual API and no errors in the logs.
// This catches rancher/k3k#559 (the immutable server StatefulSet field renamed
// by PR #869 breaks reconciliation of dynamic-persistence clusters on upgrade)
// as well as any other upgrade regression that stops a cluster from reconciling.
//
// The spec mutates the shared k3k release in k3k-system, so it is Serial and
// runs in its own dedicated test suite.
var _ = When("k3k is upgraded from the latest released version", Ordered, Serial, Label(k3kUpgradeTestsLabel), Label(slowTestsLabel), func() {
	var clusters []*upgradeCluster

	ctx := context.Background()

	BeforeAll(func() {
		// Guard early: `make install` (restore-to-source) needs these to install
		// the build under test (pushed to ttl.sh in CI) rather than a default image.
		Expect(os.Getenv("REPO")).NotTo(BeEmpty(), "REPO must be set to the image repository")
		Expect(os.Getenv("VERSION")).NotTo(BeEmpty(), "VERSION must be set to the image tag")

		By("Cleaning up any existing K3k installation and CRDs")
		cleanupK3kInstall()

		By("Installing the latest released k3k")
		helmRepoAddK3k()
		helmInstallLatestReleasedK3k()

		By("Creating a shared-mode and a virtual-mode cluster with the released k3k")

		clusters = []*upgradeCluster{
			newUpgradeCluster(v1beta1.SharedClusterMode),
			newUpgradeCluster(v1beta1.VirtualClusterMode),
		}
	})

	AfterAll(func() {
		// Best-effort diagnostic so a reconcile error (e.g. the "Forbidden"
		// StatefulSet update) is visible in CI output on failure.
		if CurrentSpecReport().Failed() {
			dumpK3kControllerLogs()
		}

		// Delete the test namespaces (non-blocking).
		for _, c := range clusters {
			if c != nil && c.virtual != nil {
				fwk3k.DeleteNamespaces(k8s, c.virtual.Cluster.Namespace)
			}
		}

		By("Cleaning up the upgraded K3k installation and CRDs before restoring source build")
		cleanupK3kInstall()

		// Restore the shared release to the build from source. Unconditional +
		// idempotent: this also repairs the case where the spec failed while still
		// on the released version.
		By("Restoring the k3k build from source")
		helmInstallSourceK3k()

		// Re-apply the coverage patch wiped by the Helm operations, so the
		// AfterSuite coverage dump still works.
		patchPVC(ctx, k8s)
	})

	It("keeps existing clusters alive and reconciling after the upgrade", func() {
		By("Upgrading k3k to the build from source")
		helmInstallSourceK3k()

		By("Waiting for the new controller rollout to complete")
		waitForControllerRollout()

		for _, c := range clusters {
			By("Patching cluster " + c.virtual.Cluster.Name + " with an annotation to force reconciliation")
			var cluster v1beta1.Cluster
			Expect(k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(c.virtual.Cluster), &cluster)).To(Succeed())
			if cluster.Annotations == nil {
				cluster.Annotations = make(map[string]string)
			}
			cluster.Annotations["k3k.io/test-upgrade-trigger"] = time.Now().Format(time.RFC3339)
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())
		}

		for _, c := range clusters {
			By("Verifying the " + string(c.virtual.Cluster.Spec.Mode) + " cluster is healthy after the upgrade")
			assertClusterHealthy(ctx, c)
		}

		By("Verifying there are no reconciliation errors in the controller logs")
		assertNoControllerErrors()
	})
})

// newUpgradeCluster provisions a dynamic-persistence cluster in the given mode
// and waits for everything to be ready. Both modes use dynamic persistence so
// the upgrade exercises the server StatefulSet path that rancher/k3k#559 breaks.
func newUpgradeCluster(mode v1beta1.ClusterMode) *upgradeCluster {
	GinkgoHelper()

	namespace := fwk3k.CreateNamespace(k8s)

	cluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
		c.Spec.Mode = mode
		c.Spec.Persistence.Type = v1beta1.DynamicPersistenceMode

		// Virtual mode needs a worker; shared mode schedules onto the host via the virtual kubelet.
		if mode == v1beta1.VirtualClusterMode {
			c.Spec.Agents = ptr.To[int32](1)
		}
	})
	CreateCluster(cluster)

	client, restConfig := NewVirtualK8sClientAndConfig(cluster)
	virtual := &VirtualCluster{Cluster: cluster, RestConfig: restConfig, Client: client}

	return &upgradeCluster{virtual: virtual}
}

// assertClusterHealthy verifies, with mode-agnostic signals, that a cluster is
// still working after the k3k upgrade: the Cluster reconciled successfully, its
// server pods are Ready, and the virtual API is reachable.
func assertClusterHealthy(ctx context.Context, c *upgradeCluster) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		// 1. The Cluster reconciles successfully with the new controller. On the
		// immutable-field bug the reconcile error is written to the status, which
		// flips Phase to Failed and the Ready condition to False, so this is the
		// signal that catches the regression.
		var cluster v1beta1.Cluster
		g.Expect(k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(c.virtual.Cluster), &cluster)).To(Succeed())
		g.Expect(cluster.Status.Phase).To(Equal(v1beta1.ClusterReady))

		cond := meta.FindStatusCondition(cluster.Status.Conditions, clustercontroller.ConditionReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))

		// 2. The server pods are Ready.
		serverPods := listServerPods(ctx, c.virtual)
		g.Expect(serverPods).NotTo(BeEmpty())

		for i := range serverPods {
			_, podCond := pod.GetPodCondition(&serverPods[i].Status, corev1.PodReady)
			g.Expect(podCond).NotTo(BeNil())
			g.Expect(podCond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}

		// 3. The virtual API is reachable.
		_, err := c.virtual.Client.Discovery().ServerVersion()
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

func cleanupK3kInstall() {
	GinkgoHelper()

	// 1. Force-delete CRDs first to clear out custom resources and strip finalizer locks immediately.
	_, _, _ = fwcmd.RunCmd("kubectl", "delete", "crd", "clusters.k3k.io", "virtualclusterpolicies.k3k.io", "--ignore-not-found", "--timeout=30s")

	// 2. Uninstall Helm release
	_, _, _ = fwcmd.RunCmd("helm", "uninstall", "k3k", "-n", k3kNamespace)
}

func helmRepoAddK3k() {
	GinkgoHelper()

	expectCmd(fwcmd.RunCmd("helm", "repo", "add", "k3k", "https://rancher.github.io/k3k", "--force-update"))
	expectCmd(fwcmd.RunCmd("helm", "repo", "update"))
}

// helmInstallLatestReleasedK3k installs the latest released k3k chart (no pinned
// version), i.e. the newest stable a user would be upgrading FROM.
func helmInstallLatestReleasedK3k() {
	GinkgoHelper()

	expectCmd(fwcmd.RunCmd("helm", "upgrade", "--install", "k3k", "k3k/k3k",
		"--namespace", k3kNamespace, "--create-namespace",
		"--wait", "--timeout", "5m",
	))
}

// helmInstallSourceK3k installs the build from source by running `make install`
// from the repo root, reusing the exact Helm flags of the Makefile install
// target (dev images from $REPO/$VERSION, ttl.sh in CI) so it can never drift
// from the original BeforeSuite install.
func helmInstallSourceK3k() {
	GinkgoHelper()

	expectCmd(fwcmd.RunCmd("make", "-C", repoRoot, "install"))
}

func waitForControllerRollout() {
	GinkgoHelper()

	expectCmd(fwcmd.RunCmd("kubectl", "rollout", "status", "deployment/k3k",
		"--namespace", k3kNamespace, "--timeout", "3m",
	))
}

func assertNoControllerErrors() {
	GinkgoHelper()

	stdout, stderr, err := fwcmd.RunCmd("kubectl", "logs", "-n", k3kNamespace, "-l", "app.kubernetes.io/name=k3k", "--tail=-1")
	Expect(err).NotTo(HaveOccurred(), stderr)

	// Verify we do not have recurring/failed reconciliation errors in the log.
	// Specifically, check for immutable field errors or standard reconcile loop failure logging formats.
	// The StatefulSet immutable update error looks like: "updates to statefulset spec for fields other than ... are forbidden"
	Expect(stdout).NotTo(ContainSubstring("forbidden: updates to statefulset spec for fields other than"), "StatefulSet update failed due to immutable field modification")
	Expect(stdout).NotTo(ContainSubstring("error reconciling"), "Found reconciliation errors in the controller logs")
}

func dumpK3kControllerLogs() {
	stdout, stderr, err := fwcmd.RunCmd("kubectl", "logs", "-n", k3kNamespace, "-l", "app.kubernetes.io/name=k3k", "--tail=-1")
	if err != nil {
		GinkgoWriter.Println("failed to collect k3k controller logs:", err, stderr)
		return
	}

	GinkgoWriter.Println("=== k3k controller logs ===")
	GinkgoWriter.Println(stdout)
}
