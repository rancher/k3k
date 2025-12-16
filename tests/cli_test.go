package k3k_test

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"

	v1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/controller/policy"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func K3kcli(args ...string) (string, string, error) {
	return runCmd("k3kcli", args...)
}

func Kubectl(args ...string) (string, string, error) {
	return runCmd("kubectl", args...)
}

func runCmd(cmdName string, args ...string) (string, string, error) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}

	cmd := exec.CommandContext(context.Background(), cmdName, args...)
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
				DeleteNamespaces(clusterNamespace)
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
			Expect(stderr).To(ContainSubstring(`Deleting '%s' cluster in namespace '%s'`, clusterName, clusterNamespace))

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

		It("can create a cluster with the specified kubernetes version", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			DeferCleanup(func() {
				DeleteNamespaces(clusterNamespace)
			})

			_, stderr, err = K3kcli("cluster", "create", "--version", "v1.33.6-k3s1", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))
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
			Expect(stderr).To(ContainSubstring(`Creating policy '%s'`, policyName))

			stdout, stderr, err = K3kcli("policy", "list")
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(BeEmpty())
			Expect(stdout).To(ContainSubstring(policyName))

			stdout, stderr, err = K3kcli("policy", "delete", policyName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stdout).To(BeEmpty())
			Expect(stderr).To(ContainSubstring(`Policy '%s' deleted`, policyName))

			stdout, stderr, err = K3kcli("policy", "list")
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stdout).To(Not(ContainSubstring(policyName)))
		})

		It("can bound a policy to a namespace", func() {
			var (
				stdout string
				stderr string
				err    error
			)

			namespaceName := "ns-" + rand.String(5)

			_, _, err = Kubectl("create", "namespace", namespaceName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))

			DeferCleanup(func() {
				DeleteNamespaces(namespaceName)
			})

			By("Creating a policy and binding to a namespace")

			policy1Name := "policy-" + rand.String(5)

			_, stderr, err = K3kcli("policy", "create", "--namespace", namespaceName, policy1Name)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Creating policy '%s'`, policy1Name))

			DeferCleanup(func() {
				stdout, stderr, err = K3kcli("policy", "delete", policy1Name)
				Expect(err).To(Not(HaveOccurred()), string(stderr))
				Expect(stdout).To(BeEmpty())
				Expect(stderr).To(ContainSubstring(`Policy '%s' deleted`, policy1Name))
			})

			var ns v1.Namespace
			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: namespaceName}, &ns)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(ns.Name).To(Equal(namespaceName))
			Expect(ns.Labels).To(HaveKeyWithValue(policy.PolicyNameLabelKey, policy1Name))

			By("Creating another policy and binding to the same namespace without the --overwrite flag")

			policy2Name := "policy-" + rand.String(5)

			stdout, stderr, err = K3kcli("policy", "create", "--namespace", namespaceName, policy2Name)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Creating policy '%s'`, policy2Name))

			DeferCleanup(func() {
				stdout, stderr, err = K3kcli("policy", "delete", policy2Name)
				Expect(err).To(Not(HaveOccurred()), string(stderr))
				Expect(stdout).To(BeEmpty())
				Expect(stderr).To(ContainSubstring(`Policy '%s' deleted`, policy2Name))
			})

			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: namespaceName}, &ns)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(ns.Name).To(Equal(namespaceName))
			Expect(ns.Labels).To(HaveKeyWithValue(policy.PolicyNameLabelKey, policy1Name))

			By("Forcing the other policy binding with the overwrite flag")

			stdout, stderr, err = K3kcli("policy", "create", "--namespace", namespaceName, "--overwrite", policy2Name)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Creating policy '%s'`, policy2Name))

			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: namespaceName}, &ns)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(ns.Name).To(Equal(namespaceName))
			Expect(ns.Labels).To(HaveKeyWithValue(policy.PolicyNameLabelKey, policy2Name))
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
				DeleteNamespaces(clusterNamespace)
			})

			_, stderr, err = K3kcli("cluster", "create", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			_, stderr, err = K3kcli("kubeconfig", "generate", "--name", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			_, stderr, err = K3kcli("cluster", "delete", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Deleting '%s' cluster in namespace '%s'`, clusterName, clusterNamespace))
		})
	})
})
