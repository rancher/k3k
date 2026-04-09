package log

import (
	"context"
	"io"
	"os"
	"path"

	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"

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
	req := clientset.CoreV1().Pods(k3kPod.Namespace).GetLogs(k3kPod.Name, &v1.PodLogOptions{Previous: true})
	podLogs, err := req.Stream(ctx)
	Expect(err).To(Not(HaveOccurred()))

	return podLogs
}

// WriteToTemp writes the provided logs to a temporary file with the specified filename.
// The file is written to os.TempDir() and the full path is logged to GinkgoWriter.
func WriteToTemp(filename string, logs io.ReadCloser) {
	GinkgoHelper()

	logsStr, err := io.ReadAll(logs)
	Expect(err).To(Not(HaveOccurred()))

	tempfile := path.Join(os.TempDir(), filename)
	err = os.WriteFile(tempfile, logsStr, 0o644)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Println("logs written to: " + tempfile)

	_ = logs.Close()
}
