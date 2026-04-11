package k3k

import (
	"context"
	"fmt"
	"os"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// CreateNamespace creates a new namespace with a generated name and the "e2e: true" label.
// The namespace is created using the provided Kubernetes clientset.
func CreateNamespace(clientset kubernetes.Interface) *v1.Namespace {
	GinkgoHelper()

	namespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "ns-",
			Labels: map[string]string{
				"e2e": "true",
			},
		},
	}

	namespace, err := clientset.CoreV1().Namespaces().Create(context.Background(), namespace, metav1.CreateOptions{})
	Expect(err).To(Not(HaveOccurred()))

	return namespace
}

// DeleteNamespaces deletes the specified namespaces in parallel.
// If the KEEP_NAMESPACES environment variable is set, namespaces are preserved instead.
// This is useful for debugging test failures.
func DeleteNamespaces(clientset kubernetes.Interface, names ...string) {
	GinkgoHelper()

	if _, found := os.LookupEnv("KEEP_NAMESPACES"); found {
		By(fmt.Sprintf("Keeping namespaces %v", names))
		return
	}

	wg := sync.WaitGroup{}
	wg.Add(len(names))

	for _, name := range names {
		go func() {
			defer wg.Done()
			defer GinkgoRecover()

			By(fmt.Sprintf("Deleting namespace %s", name))

			err := clientset.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{
				GracePeriodSeconds: ptr.To[int64](0),
			})
			Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))
		}()
	}

	wg.Wait()
}
