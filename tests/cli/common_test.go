package cli_test

import (
	"context"
	"fmt"
	"os"
	"sync"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func NewNamespace() *v1.Namespace {
	GinkgoHelper()

	namespace := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-", Labels: map[string]string{"e2e": "true"}}}
	namespace, err := k8s.CoreV1().Namespaces().Create(context.Background(), namespace, metav1.CreateOptions{})
	Expect(err).To(Not(HaveOccurred()))

	return namespace
}

func DeleteNamespaces(names ...string) {
	GinkgoHelper()

	if _, found := os.LookupEnv("KEEP_NAMESPACES"); found {
		By(fmt.Sprintf("Keeping namespace %v", names))
		return
	}

	wg := sync.WaitGroup{}
	wg.Add(len(names))

	for _, name := range names {
		go func() {
			defer wg.Done()
			defer GinkgoRecover()

			By(fmt.Sprintf("Deleting namespace %s", name))

			err := k8s.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{
				GracePeriodSeconds: ptr.To[int64](0),
			})
			Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))
		}()
	}

	wg.Wait()
}
