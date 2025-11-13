package k3k_test

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func K3kcli(args ...string) (string, string, error) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}

	cmd := exec.CommandContext(context.Background(), "k3kcli", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

var _ = When("using the k3kcli", Label("cli"), func() {
	It("can get the version", func() {
		stdout, _, err := K3kcli("--version")
		Expect(err).To(Not(HaveOccurred()))
		Expect(stdout).To(ContainSubstring("k3kcli version v"))
	})

	When("trying the cluster commands", func() {
		It("can create, list and delete a cluster", func() {
			var (
				stdout string
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			DeferCleanup(func() {
				err := k8sClient.Delete(context.Background(), &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: clusterNamespace,
					},
				})
				Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))
			})

			_, stderr, err = K3kcli("cluster", "create", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			stdout, stderr, err = K3kcli("cluster", "list")
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(BeEmpty())
			Expect(stdout).To(ContainSubstring(clusterNamespace))

			_, stderr, err = K3kcli("cluster", "delete", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Deleting %q cluster in namespace %q`, clusterName, clusterNamespace))

			// The deletion could take a bit
			Eventually(func() string {
				stdout, stderr, err := K3kcli("cluster", "list", "-n", clusterNamespace)
				Expect(err).To(Not(HaveOccurred()), string(stderr))
				return stdout + stderr
			}).
				WithTimeout(time.Second * 5).
				WithPolling(time.Second).
				Should(BeEmpty())
		})
	})

	When("trying the policy commands", func() {
		It("can create, list and delete a policy", func() {
			var (
				stdout string
				stderr string
				err    error
			)

			policyName := "policy-" + rand.String(5)

			_, stderr, err = K3kcli("policy", "create", policyName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Creating policy %q`, policyName))

			stdout, stderr, err = K3kcli("policy", "list")
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(BeEmpty())
			Expect(stdout).To(ContainSubstring(policyName))

			stdout, stderr, err = K3kcli("policy", "delete", policyName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stdout).To(BeEmpty())
			Expect(stderr).To(BeEmpty())

			stdout, stderr, err = K3kcli("policy", "list")
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stdout).To(BeEmpty())
			Expect(stderr).To(BeEmpty())
		})
	})

	When("trying the kubeconfig command", func() {
		It("can generate a kubeconfig", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			DeferCleanup(func() {
				err := k8sClient.Delete(context.Background(), &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: clusterNamespace,
					},
				})
				Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))
			})

			_, stderr, err = K3kcli("cluster", "create", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			_, stderr, err = K3kcli("kubeconfig", "generate", "--name", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			_, stderr, err = K3kcli("cluster", "delete", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Deleting %q cluster in namespace %q`, clusterName, clusterNamespace))
		})
	})
})
