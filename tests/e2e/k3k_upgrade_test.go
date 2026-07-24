package k3k_test

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

// repoRoot is the k3k repository root relative to the tests/e2e package dir,
// used to run `make install` (which installs the build from source).
const repoRoot = "../.."

// upgradeCluster bundles a virtual cluster created before the upgrade with a
// workload deployed inside it, so we can assert both survive the upgrade.
type upgradeCluster struct {
	virtual  *VirtualCluster
	nginxPod *corev1.Pod
}

// This is a generic smoke test for upgrading the k3k controller itself: it
// installs the latest released k3k, provisions a shared-mode and a virtual-mode
// cluster (both with dynamic persistence and a running workload), then upgrades
// k3k to the build from source and verifies every cluster is still alive and
// reconciling successfully.
//
// It is deliberately mode-agnostic and does not assert on any single controller
// implementation detail: the signal is the reconciled Cluster status, the server
// pods, the reachability of the virtual API and the survival of the workload.
// This catches rancher/k3k#559 (the immutable server StatefulSet field renamed
// by PR #869 breaks reconciliation of dynamic-persistence clusters on upgrade)
// as well as any other upgrade regression that stops a cluster from reconciling.
//
// The spec mutates the shared k3k release in k3k-system, so it is Serial and
// runs in its own CI matrix bucket. AfterAll restores the dev build and
// re-applies the coverage patch so the rest of the suite (and the AfterSuite
// coverage dump) keeps working.
var _ = When("k3k is upgraded from the latest released version", Ordered, Serial, Label(k3kUpgradeTestsLabel), Label(slowTestsLabel), func() {
	var clusters []*upgradeCluster

	ctx := context.Background()

	BeforeAll(func() {
		// Guard early: `make install` (restore-to-source) needs these to install
		// the build under test (pushed to ttl.sh in CI) rather than a default image.
		Expect(os.Getenv("REPO")).NotTo(BeEmpty(), "REPO must be set to the image repository")
		Expect(os.Getenv("VERSION")).NotTo(BeEmpty(), "VERSION must be set to the image tag")

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

		for _, c := range clusters {
			By("Verifying the " + string(c.virtual.Cluster.Spec.Mode) + " cluster is healthy after the upgrade")
			assertClusterHealthy(ctx, c)
		}
	})
})

// newUpgradeCluster provisions a dynamic-persistence cluster in the given mode
// with a workload running inside it, and waits for everything to be ready. Both
// modes use dynamic persistence so the upgrade exercises the server StatefulSet
// path that rancher/k3k#559 breaks.
func newUpgradeCluster(mode v1beta1.ClusterMode) *upgradeCluster {
	GinkgoHelper()

	namespace := fwk3k.CreateNamespace(k8s)

	cluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
		c.Spec.Mode = mode
		c.Spec.Persistence.Type = v1beta1.DynamicPersistenceMode

		// Virtual mode needs a worker to schedule the workload; shared mode
		// schedules onto the host via the virtual kubelet.
		if mode == v1beta1.VirtualClusterMode {
			c.Spec.Agents = ptr.To[int32](1)
		}
	})
	CreateCluster(cluster)

	client, restConfig := NewVirtualK8sClientAndConfig(cluster)
	virtual := &VirtualCluster{Cluster: cluster, RestConfig: restConfig, Client: client}

	nginxPod, _ := virtual.NewNginxPod("")

	return &upgradeCluster{virtual: virtual, nginxPod: nginxPod}
}

// assertClusterHealthy verifies, with mode-agnostic signals, that a cluster is
// still working after the k3k upgrade: the Cluster reconciled successfully, its
// server pods are Ready, the virtual API is reachable and the workload survived.
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

		// 4. The workload deployed before the upgrade is still Ready.
		nginxPod, err := c.virtual.Client.CoreV1().Pods(c.nginxPod.Namespace).Get(ctx, c.nginxPod.Name, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())

		_, nginxCond := pod.GetPodCondition(&nginxPod.Status, corev1.PodReady)
		g.Expect(nginxCond).NotTo(BeNil())
		g.Expect(nginxCond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
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

func dumpK3kControllerLogs() {
	stdout, stderr, err := fwcmd.RunCmd("kubectl", "logs", "-n", k3kNamespace, "-l", "app.kubernetes.io/name=k3k", "--tail=-1")
	if err != nil {
		GinkgoWriter.Println("failed to collect k3k controller logs:", err, stderr)
		return
	}

	GinkgoWriter.Println("=== k3k controller logs ===")
	GinkgoWriter.Println(stdout)
}
