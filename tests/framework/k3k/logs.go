package k3k

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"k8s.io/client-go/kubernetes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
)

// failureLogsDir is the directory where the namespace resources of the failed specs are dumped.
// It's archived by the CI on failure.
const failureLogsDir = "/tmp/e2e-failure-logs"

// dumpFailedSpecNamespace dumps the namespace resources if the current spec failed.
// Errors are only reported, to avoid failing the cleanup of the namespace.
func dumpFailedSpecNamespace(clientset kubernetes.Interface, namespace string) {
	if !CurrentSpecReport().Failed() {
		return
	}

	dir := filepath.Join(failureLogsDir, namespace)

	By(fmt.Sprintf("Dumping namespace %s to %s", namespace, dir))

	if err := DumpNamespace(context.Background(), clientset, namespace, dir); err != nil {
		GinkgoWriter.Printf("failed to dump namespace %s: %v\n", namespace, err)
	}
}

// DumpNamespace writes to dir the status of the pods and the events of the namespace, together
// with the logs of all the containers, including the previous ones of the restarted containers.
func DumpNamespace(ctx context.Context, clientset kubernetes.Interface, namespace, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	spec := fmt.Sprintf("%s\n%s\n", CurrentSpecReport().FullText(), CurrentSpecReport().FailureMessage())
	if err := os.WriteFile(filepath.Join(dir, "spec.txt"), []byte(spec), 0o644); err != nil {
		return err
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	var errs []error

	if err := os.WriteFile(filepath.Join(dir, "pods.txt"), []byte(podsSummary(pods.Items)), 0o644); err != nil {
		errs = append(errs, err)
	}

	if err := dumpEvents(ctx, clientset, namespace, dir); err != nil {
		errs = append(errs, err)
	}

	for _, pod := range pods.Items {
		statuses := slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses)

		for _, status := range statuses {
			errs = append(errs, dumpContainerLogs(ctx, clientset, &pod, status.Name, false, dir))

			// the logs of the previous container hold the reason of a restart (i.e. a crash)
			if status.RestartCount > 0 {
				errs = append(errs, dumpContainerLogs(ctx, clientset, &pod, status.Name, true, dir))
			}
		}
	}

	return errors.Join(errs...)
}

// podsSummary returns the phase of the pods, with the state and the restarts of their containers.
func podsSummary(pods []corev1.Pod) string {
	var sb strings.Builder

	for _, pod := range pods {
		fmt.Fprintf(&sb, "pod %s phase=%s node=%s\n", pod.Name, pod.Status.Phase, pod.Spec.NodeName)

		statuses := slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses)

		for _, status := range statuses {
			fmt.Fprintf(&sb, "  container %s ready=%t restarts=%d image=%s\n", status.Name, status.Ready, status.RestartCount, status.Image)

			if waiting := status.State.Waiting; waiting != nil {
				fmt.Fprintf(&sb, "    waiting: %s %s\n", waiting.Reason, waiting.Message)
			}

			if terminated := status.LastTerminationState.Terminated; terminated != nil {
				fmt.Fprintf(&sb, "    last terminated: reason=%s exitCode=%d finishedAt=%s %s\n",
					terminated.Reason, terminated.ExitCode, terminated.FinishedAt.UTC().Format("15:04:05"), terminated.Message)
			}
		}
	}

	return sb.String()
}

// dumpEvents writes the events of the namespace, sorted by time.
// They include the probe failures and the container kills, that are not logged by the kubelet.
func dumpEvents(ctx context.Context, clientset kubernetes.Interface, namespace, dir string) error {
	events, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	eventTime := func(event corev1.Event) metav1.Time {
		if !event.LastTimestamp.IsZero() {
			return event.LastTimestamp
		}

		return metav1.NewTime(event.EventTime.Time)
	}

	slices.SortFunc(events.Items, func(a, b corev1.Event) int {
		return eventTime(a).Compare(eventTime(b).Time)
	})

	var sb strings.Builder

	for _, event := range events.Items {
		fmt.Fprintf(&sb, "%s %s %s %s/%s (x%d): %s\n",
			eventTime(event).UTC().Format("15:04:05"), event.Type, event.Reason,
			event.InvolvedObject.Kind, event.InvolvedObject.Name, max(event.Count, 1), event.Message)
	}

	return os.WriteFile(filepath.Join(dir, "events.txt"), []byte(sb.String()), 0o644)
}

// dumpContainerLogs writes the logs of a container to the <pod>_<container>[_previous].log file.
func dumpContainerLogs(ctx context.Context, clientset kubernetes.Interface, pod *corev1.Pod, container string, previous bool, dir string) error {
	logs, err := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container: container,
		Previous:  previous,
	}).DoRaw(ctx)
	if err != nil {
		return fmt.Errorf("logs of %s/%s (previous=%t): %w", pod.Name, container, previous, err)
	}

	name := pod.Name + "_" + container
	if previous {
		name += "_previous"
	}

	return os.WriteFile(filepath.Join(dir, name+".log"), logs, 0o644)
}
