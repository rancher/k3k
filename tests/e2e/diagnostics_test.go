package e2e_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
)

// diagnosticsDirEnv sets where the diagnostics of failed specs are written.
const diagnosticsDirEnv = "E2E_DIAGNOSTICS_DIR"

// This JustAfterEach runs for every spec in the suite. Ginkgo runs it before any AfterEach,
// nested ones included, and before the DeferCleanup callbacks, so the test namespaces have
// not been deleted yet when it dumps them.
var _ = JustAfterEach(func() {
	report := CurrentSpecReport()
	if !report.Failed() {
		return
	}

	dir := filepath.Join(diagnosticsDir(), specDirName(report))

	By("Dumping diagnostics of the failed spec to " + dir)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dumpTestNamespaces(ctx, dir)
})

func diagnosticsDir() string {
	if dir := os.Getenv(diagnosticsDirEnv); dir != "" {
		return dir
	}

	return filepath.Join(os.TempDir(), "e2e-diagnostics")
}

var unsafePathChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// specDirName returns a directory name unique to the spec and its attempt, so the retries
// that --flake-attempts makes don't overwrite each other.
func specDirName(report SpecReport) string {
	name := unsafePathChars.ReplaceAllString(report.FullText(), "-")
	name = strings.Trim(name, "-")

	if len(name) > 150 {
		name = name[:150]
	}

	return fmt.Sprintf("%s-attempt-%d", name, report.NumAttempts)
}

// dumpTestNamespaces writes the clusters, pods, events and container logs of every
// namespace that the suite created and that is not already being deleted.
// It is best effort: errors are logged and don't fail the spec any further.
func dumpTestNamespaces(ctx context.Context, dir string) {
	var namespaces corev1.NamespaceList
	if err := k8sClient.List(ctx, &namespaces, client.MatchingLabels{"e2e": "true"}); err != nil {
		GinkgoWriter.Printf("diagnostics: listing namespaces: %v\n", err)
		return
	}

	for _, ns := range namespaces.Items {
		if ns.DeletionTimestamp != nil {
			continue
		}

		dumpNamespace(ctx, filepath.Join(dir, ns.Name), ns.Name)
	}
}

func dumpNamespace(ctx context.Context, dir, namespace string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		GinkgoWriter.Printf("diagnostics: creating %s: %v\n", dir, err)
		return
	}

	var clusters v1beta1.ClusterList
	if err := k8sClient.List(ctx, &clusters, client.InNamespace(namespace)); err != nil {
		GinkgoWriter.Printf("diagnostics: listing clusters in %s: %v\n", namespace, err)
	} else {
		writeYAML(filepath.Join(dir, "clusters.yaml"), &clusters)
	}

	var pods corev1.PodList
	if err := k8sClient.List(ctx, &pods, client.InNamespace(namespace)); err != nil {
		GinkgoWriter.Printf("diagnostics: listing pods in %s: %v\n", namespace, err)
	} else {
		writeYAML(filepath.Join(dir, "pods.yaml"), &pods)
		writeFile(filepath.Join(dir, "pods-summary.txt"), podsSummary(pods.Items))

		for _, pod := range pods.Items {
			dumpPodLogs(ctx, filepath.Join(dir, "logs"), &pod)
		}
	}

	var events corev1.EventList
	if err := k8sClient.List(ctx, &events, client.InNamespace(namespace)); err != nil {
		GinkgoWriter.Printf("diagnostics: listing events in %s: %v\n", namespace, err)
	} else {
		writeFile(filepath.Join(dir, "events.txt"), eventsSummary(events.Items))
	}
}

// podsSummary shows at a glance the restarts of the containers and why they last terminated,
// e.g. an exit code or a liveness probe kill, which is hard to spot in the full pods.yaml.
func podsSummary(pods []corev1.Pod) string {
	var sb strings.Builder

	for _, pod := range pods {
		fmt.Fprintf(&sb, "pod %s phase=%s node=%s ip=%s\n", pod.Name, pod.Status.Phase, pod.Spec.NodeName, pod.Status.PodIP)

		for _, cond := range pod.Status.Conditions {
			fmt.Fprintf(&sb, "  condition %s=%s reason=%q message=%q\n", cond.Type, cond.Status, cond.Reason, cond.Message)
		}

		for _, cs := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
			fmt.Fprintf(&sb, "  container %s image=%s ready=%t restarts=%d state=%s\n",
				cs.Name, cs.Image, cs.Ready, cs.RestartCount, containerState(cs.State))

			if cs.LastTerminationState.Terminated != nil {
				fmt.Fprintf(&sb, "    last state=%s\n", containerState(cs.LastTerminationState))
			}
		}
	}

	return sb.String()
}

func containerState(state corev1.ContainerState) string {
	switch {
	case state.Running != nil:
		return "running since " + state.Running.StartedAt.UTC().Format(time.RFC3339)
	case state.Waiting != nil:
		return fmt.Sprintf("waiting reason=%q message=%q", state.Waiting.Reason, state.Waiting.Message)
	case state.Terminated != nil:
		t := state.Terminated

		return fmt.Sprintf("terminated reason=%q exitCode=%d signal=%d started=%s finished=%s message=%q",
			t.Reason, t.ExitCode, t.Signal,
			t.StartedAt.UTC().Format(time.RFC3339), t.FinishedAt.UTC().Format(time.RFC3339), t.Message)
	default:
		return "unknown"
	}
}

// eventsSummary lists the events oldest first. The events include the liveness and readiness
// probe failures, and the kills of containers that failed their liveness probe.
func eventsSummary(events []corev1.Event) string {
	slices.SortStableFunc(events, func(a, b corev1.Event) int {
		return eventTime(a).Compare(eventTime(b))
	})

	var sb strings.Builder

	for _, e := range events {
		fmt.Fprintf(&sb, "%s %s %s/%s %s (x%d): %s\n",
			eventTime(e).UTC().Format(time.RFC3339), e.Type, e.InvolvedObject.Kind, e.InvolvedObject.Name,
			e.Reason, max(e.Count, 1), e.Message)
	}

	return sb.String()
}

func eventTime(e corev1.Event) time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	default:
		return e.CreationTimestamp.Time
	}
}

// dumpPodLogs writes the logs of the current and, if any, of the previous instance of every
// container. The previous logs explain why a crash looping container exited.
func dumpPodLogs(ctx context.Context, dir string, pod *corev1.Pod) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		GinkgoWriter.Printf("diagnostics: creating %s: %v\n", dir, err)
		return
	}

	for _, cs := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		base := filepath.Join(dir, pod.Name+"_"+cs.Name)

		writePodLogs(ctx, base+".log", pod, cs.Name, false)

		if cs.RestartCount > 0 {
			writePodLogs(ctx, base+".previous.log", pod, cs.Name, true)
		}
	}
}

func writePodLogs(ctx context.Context, path string, pod *corev1.Pod, container string, previous bool) {
	stream, err := k8s.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container:  container,
		Previous:   previous,
		Timestamps: true,
	}).Stream(ctx)
	if err != nil {
		GinkgoWriter.Printf("diagnostics: getting logs of %s/%s (previous=%t): %v\n", pod.Name, container, previous, err)
		return
	}
	defer func() { _ = stream.Close() }()

	f, err := os.Create(path)
	if err != nil {
		GinkgoWriter.Printf("diagnostics: creating %s: %v\n", path, err)
		return
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, stream); err != nil {
		GinkgoWriter.Printf("diagnostics: writing %s: %v\n", path, err)
	}
}

func writeYAML(path string, obj any) {
	data, err := yaml.Marshal(obj)
	if err != nil {
		GinkgoWriter.Printf("diagnostics: marshalling %s: %v\n", path, err)
		return
	}

	writeFile(path, string(data))
}

func writeFile(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		GinkgoWriter.Printf("diagnostics: writing %s: %v\n", path, err)
	}
}
