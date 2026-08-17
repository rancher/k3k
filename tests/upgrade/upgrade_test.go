package upgrade_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// upgradeCluster is a Cluster provisioned by the released k3k, together with the
// app that was deployed in it before the upgrade.
type upgradeCluster struct {
	cluster *v1beta1.Cluster
	client  *kubernetes.Clientset

	// appPodUIDs are the UIDs of the app pods as they were before the upgrade.
	appPodUIDs []types.UID
}

func (c *upgradeCluster) name() string {
	return fmt.Sprintf("%s (%s mode)", c.cluster.Name, c.cluster.Spec.Mode)
}

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
		clusters   []*upgradeCluster
		namespaces []string
	)

	ctx := context.Background()

	BeforeAll(func() {
		// Guard early: `make install` (the upgrade under test) must install the build
		// under test, not the default `rancher/k3k` image. REPO has to point at the
		// images built from this checkout and available to every node (in CI they are
		// tagged `k3k.local/...` and imported into containerd directly).
		Expect(os.Getenv("REPO")).NotTo(BeEmpty(), "REPO must be set to the image repository of the build under test")

		By("Cleaning up any existing K3k installation and CRDs")
		cleanupK3kInstall()

		By("Installing the latest released k3k")
		helmRepoAddK3k()
		helmInstallLatestReleasedK3k()

		By("Creating a shared-mode and a virtual-mode cluster with the released k3k")

		clusters = []*upgradeCluster{
			newUpgradeCluster(ctx, v1beta1.SharedClusterMode),
			newUpgradeCluster(ctx, v1beta1.VirtualClusterMode),
		}

		for _, c := range clusters {
			namespaces = append(namespaces, c.cluster.Namespace)

			By("Deploying the app in cluster " + c.name())
			deployApp(ctx, c.client)

			c.appPodUIDs = listAppPodUIDs(ctx, c.client)
			Expect(c.appPodUIDs).To(HaveLen(appReplicas))
		}

		By("Upgrading k3k to the build from source")
		helmInstallSourceK3k()

		By("Waiting for the new controller rollout to complete")
		waitForControllerRollout()
	})

	AfterAll(func() {
		// Best-effort diagnostic so a reconcile error (e.g. the "Forbidden"
		// StatefulSet update) is visible in CI output on failure.
		if CurrentSpecReport().Failed() {
			dumpK3kControllerLogs()
		}

		fwk3k.DeleteNamespaces(k8s, namespaces...)

		By("Cleaning up the upgraded K3k installation and CRDs")
		cleanupK3kInstall()

		// Restore the shared release to the build from source. Unconditional +
		// idempotent: this also repairs the case where the spec failed while still
		// on the released version.
		By("Restoring the k3k build from source")
		helmInstallSourceK3k()
	})

	It("keeps the existing clusters healthy", func() {
		for _, c := range clusters {
			By("Verifying the cluster " + c.name() + " is healthy after the upgrade")
			assertClusterHealthy(ctx, c)
		}

		By("Verifying the controller did not fail to update an immutable field")
		assertNoImmutableFieldErrors()
	})

	It("keeps the existing workloads untouched", func() {
		for _, c := range clusters {
			By("Verifying the app in cluster " + c.name() + " is still available")
			assertAppAvailable(ctx, c.client)

			// The upgrade replaces the controller (and, in shared mode, the virtual
			// kubelet), but it must never recreate the pods of a user workload.
			By("Verifying the app pods in cluster " + c.name() + " were not recreated")
			Expect(listAppPodUIDs(ctx, c.client)).To(ConsistOf(c.appPodUIDs))
		}
	})

	It("still reconciles clusters created by the previous version", func() {
		// Mutating the server args forces an update of the server StatefulSet that
		// was created by the released controller: this is where an immutable field
		// regression such as rancher/k3k#559 surfaces.
		const serverArg = "--node-label=test_server=upgraded"

		for _, c := range clusters {
			By("Updating the server args of cluster " + c.name())

			Eventually(func(g Gomega) {
				var cluster v1beta1.Cluster

				g.Expect(k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(c.cluster), &cluster)).To(Succeed())

				cluster.Spec.ServerArgs = []string{serverArg}

				g.Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		}

		for _, c := range clusters {
			By("Verifying the servers of cluster " + c.name() + " rolled out with the new args")

			Eventually(func(g Gomega) {
				serverPods := fw.ListServerPods(ctx, c.cluster)
				g.Expect(serverPods).NotTo(BeEmpty())

				for i := range serverPods {
					g.Expect(hasArg(&serverPods[i], serverArg)).To(BeTrue(), "server pod %s does not have the new arg yet", serverPods[i].Name)

					_, cond := pod.GetPodCondition(&serverPods[i].Status, corev1.PodReady)
					g.Expect(cond).NotTo(BeNil())
					g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
				}
			}).
				WithTimeout(time.Minute * 5).
				WithPolling(time.Second * 5).
				Should(Succeed())

			assertClusterHealthy(ctx, c)
		}

		By("Verifying the controller did not fail to update an immutable field")
		assertNoImmutableFieldErrors()
	})

	It("can still create new clusters", func() {
		for _, mode := range []v1beta1.ClusterMode{v1beta1.SharedClusterMode, v1beta1.VirtualClusterMode} {
			By("Creating a new " + string(mode) + " cluster with the upgraded k3k")

			c := newUpgradeCluster(ctx, mode)
			namespaces = append(namespaces, c.cluster.Namespace)

			assertClusterHealthy(ctx, c)
		}
	})

	It("can still delete clusters created by the previous version", func() {
		c := clusters[0]

		By("Deleting the cluster " + c.name())
		Expect(k8sClient.Delete(ctx, c.cluster)).To(Succeed())

		Eventually(func(g Gomega) {
			var cluster v1beta1.Cluster

			err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(c.cluster), &cluster)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cluster still exists, finalizers may be stuck")

			// the host resources of the cluster should be garbage collected as well
			g.Expect(fw.ListServerPods(ctx, c.cluster)).To(BeEmpty())
		}).
			WithTimeout(time.Minute * 3).
			WithPolling(time.Second * 5).
			Should(Succeed())
	})
})

// newUpgradeCluster provisions a dynamic-persistence cluster in the given mode,
// in a new namespace, and waits for everything to be ready. Both modes use dynamic
// persistence so that the upgrade exercises the server StatefulSet path that
// rancher/k3k#559 breaks.
func newUpgradeCluster(ctx context.Context, mode v1beta1.ClusterMode) *upgradeCluster {
	GinkgoHelper()

	namespace := fwk3k.CreateNamespace(k8s)

	cluster := fw.NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
		c.Spec.Mode = mode
		c.Spec.Persistence.Type = v1beta1.DynamicPersistenceMode

		// Virtual mode needs a worker; shared mode schedules onto the host via the virtual kubelet.
		if mode == v1beta1.VirtualClusterMode {
			c.Spec.Agents = ptr.To[int32](1)
		}
	})

	fw.CreateCluster(ctx, cluster)

	client, _, _ := fw.NewVirtualK8sClient(ctx, cluster)

	return &upgradeCluster{cluster: cluster, client: client}
}

// assertClusterHealthy verifies, with mode-agnostic signals, that a cluster is
// working: the Cluster reconciled successfully, its server pods are Ready, and
// the virtual API is reachable.
func assertClusterHealthy(ctx context.Context, c *upgradeCluster) {
	GinkgoHelper()

	cluster := c.cluster

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
		serverPods := fw.ListServerPods(ctx, cluster)
		g.Expect(serverPods).NotTo(BeEmpty())

		for i := range serverPods {
			_, podCond := pod.GetPodCondition(&serverPods[i].Status, corev1.PodReady)
			g.Expect(podCond).NotTo(BeNil())
			g.Expect(podCond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}

		// 3. The virtual API is reachable.
		_, err := c.client.Discovery().ServerVersion()
		g.Expect(err).NotTo(HaveOccurred())
	}).
		WithTimeout(time.Minute * 3).
		WithPolling(time.Second * 5).
		Should(Succeed())
}

// hasArg returns true if the argument is found in the command of the first container of the pod.
func hasArg(p *corev1.Pod, arg string) bool {
	for _, cmd := range p.Spec.Containers[0].Command {
		if strings.Contains(cmd, arg) {
			return true
		}
	}

	return false
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
// version, no pre-releases), i.e. the newest stable a user would be upgrading FROM.
func helmInstallLatestReleasedK3k() {
	GinkgoHelper()

	expectCmd(fwcmd.RunCmd("helm", "upgrade", "--install", "k3k", "k3k/k3k",
		"--namespace", k3kNamespace, "--create-namespace",
		"--wait", "--timeout", "5m",
	))
}

// helmInstallSourceK3k installs the build from source by running `make install`
// from the repo root, reusing the exact Helm flags of the Makefile install target
// (dev images from $REPO/$VERSION) so it can never drift from a manual installation.
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

// assertNoImmutableFieldErrors checks the controller logs for the failure mode of
// rancher/k3k#559: the controller trying to update an immutable field of a
// StatefulSet created by the previous version.
//
// Only this specific error is asserted on: the controller logs transient errors
// (conflicts, not-found on freshly created objects) during normal operation, so a
// broader check would be flaky.
func assertNoImmutableFieldErrors() {
	GinkgoHelper()

	stdout, stderr, err := fwcmd.RunCmd("kubectl", "logs", "-n", k3kNamespace, "-l", "app.kubernetes.io/name=k3k", "--tail=-1")
	Expect(err).NotTo(HaveOccurred(), stderr)

	// The StatefulSet immutable update error looks like:
	// "updates to statefulset spec for fields other than ... are forbidden"
	Expect(stdout).NotTo(ContainSubstring("forbidden: updates to statefulset spec for fields other than"),
		"StatefulSet update failed due to immutable field modification")
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
