package log

import (
	"context"
	"io"
	"os"
	"path"

	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// GetK3kPodLogs retrieves logs from the first k3k pod in the specified namespace.
// This is useful for debugging test failures.
func GetK3kPodLogs(ctx context.Context, k8sClient client.Client, clientset kubernetes.Interface, namespace string) io.ReadCloser {
	GinkgoHelper()

	var podList v1.PodList

	err := k8sClient.List(ctx, &podList, &client.ListOptions{Namespace: namespace})
	Expect(err).To(Not(HaveOccurred()))
	Expect(podList.Items).NotTo(BeEmpty())

	k3kPod := podList.Items[0]

	// Fetch complete pod object to access status information
	pod, err := clientset.CoreV1().Pods(k3kPod.Namespace).Get(ctx, k3kPod.Name, metav1.GetOptions{})
	Expect(err).To(Not(HaveOccurred()))

	// Detect if the container has been restarted (e.g., for coverage dumping in E2E tests)
	fetchPrevious := false

	if len(pod.Status.ContainerStatuses) > 0 {
		containerStatus := pod.Status.ContainerStatuses[0]

		if containerStatus.RestartCount > 0 {
			fetchPrevious = true

			GinkgoWriter.Printf("Container has been restarted %d time(s), fetching previous logs\n", containerStatus.RestartCount)
		}
	}

	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &v1.PodLogOptions{Previous: fetchPrevious})
	podLogs, err := req.Stream(ctx)
	Expect(err).To(Not(HaveOccurred()))

	return podLogs
}

// WriteLogs writes the provided logs to a temporary file with the specified filename.
// The file is written to os.TempDir() and the full path is logged to GinkgoWriter.
func WriteLogs(filename string, logs io.ReadCloser) {
	GinkgoHelper()

	logsStr, err := io.ReadAll(logs)
	Expect(err).To(Not(HaveOccurred()))

	tempfile := path.Join(os.TempDir(), filename)
	err = os.WriteFile(tempfile, logsStr, 0o644)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Println("logs written to: " + tempfile)

	_ = logs.Close()
}
