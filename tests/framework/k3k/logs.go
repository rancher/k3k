package k3k

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
)

// failureLogsDir is the directory where the namespace resources of the failed specs are dumped.
// It's archived by the CI on failure.
const failureLogsDir = "/tmp/e2e-failure-logs"

// dumpFailedSpecNamespace dumps the pods, the events and the container logs of the namespace
// if the current spec failed. Errors are only reported, to avoid failing the cleanup of the namespace.
func dumpFailedSpecNamespace(clientset kubernetes.Interface, namespace string) {
	if !CurrentSpecReport().Failed() {
		return
	}

	dir := filepath.Join(failureLogsDir, namespace)

	By(fmt.Sprintf("Dumping namespace %s to %s", namespace, dir))

	if err := dumpNamespace(context.Background(), clientset, namespace, dir); err != nil {
		GinkgoWriter.Printf("failed to dump namespace %s: %v\n", namespace, err)
	}
}

func dumpNamespace(ctx context.Context, clientset kubernetes.Interface, namespace, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	events, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	spec := CurrentSpecReport().FullText() + "\n" + CurrentSpecReport().FailureMessage()

	errs := []error{
		os.WriteFile(filepath.Join(dir, "spec.txt"), []byte(spec), 0o644),
		writeYAML(filepath.Join(dir, "pods.yaml"), pods),
		writeYAML(filepath.Join(dir, "events.yaml"), events),
	}

	for _, pod := range pods.Items {
		for _, status := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
			errs = append(errs, dumpContainerLogs(ctx, clientset, &pod, status.Name, false, dir))

			// the logs of the previous container hold the reason of a restart (i.e. a crash)
			if status.RestartCount > 0 {
				errs = append(errs, dumpContainerLogs(ctx, clientset, &pod, status.Name, true, dir))
			}
		}
	}

	return errors.Join(errs...)
}

func writeYAML(path string, obj any) error {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
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
