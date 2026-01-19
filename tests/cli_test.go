package k3k_test

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"

	v1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
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

	When("trying the cluster update commands", func() {
		It("can update a cluster's server count", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			DeferCleanup(func() {
				DeleteNamespaces(clusterNamespace)
			})

			// Create the cluster first
			_, stderr, err = K3kcli("cluster", "create", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			// Update the cluster server count
			_, stderr, err = K3kcli("cluster", "update", "-y", "--servers", "2", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("Updating cluster"))

			// Verify the cluster state was actually updated
			var cluster v1beta1.Cluster
			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster)
			Expect(err).To(Not(HaveOccurred()))
			Expect(cluster.Spec.Servers).To(Not(BeNil()))
			Expect(*cluster.Spec.Servers).To(Equal(int32(2)))
		})

		It("can update a cluster's version", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			DeferCleanup(func() {
				DeleteNamespaces(clusterNamespace)
			})

			// Create the cluster with initial version
			_, stderr, err = K3kcli("cluster", "create", "--version", "v1.31.13-k3s1", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			// Update the cluster version
			_, stderr, err = K3kcli("cluster", "update", "-y", "--version", "v1.32.8-k3s1", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("Updating cluster"))

			// Verify the cluster state was actually updated
			var cluster v1beta1.Cluster
			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster)
			Expect(err).To(Not(HaveOccurred()))
			Expect(cluster.Spec.Version).To(Equal("v1.32.8-k3s1"))
		})

		It("fails to downgrade cluster version", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			DeferCleanup(func() {
				DeleteNamespaces(clusterNamespace)
			})

			// Create the cluster with a version
			_, stderr, err = K3kcli("cluster", "create", "--version", "v1.32.8-k3s1", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			// Attempt to downgrade should fail
			_, stderr, err = K3kcli("cluster", "update", "-y", "--version", "v1.31.13-k3s1", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(HaveOccurred())
			Expect(stderr).To(ContainSubstring("downgrading cluster version is not supported"))

			// Verify the cluster version was NOT changed
			var cluster v1beta1.Cluster
			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster)
			Expect(err).To(Not(HaveOccurred()))
			Expect(cluster.Spec.Version).To(Equal("v1.32.8-k3s1"))
		})

		It("fails to update a non-existent cluster", func() {
			var (
				stderr string
				err    error
			)

			// Attempt to update a cluster that doesn't exist
			_, stderr, err = K3kcli("cluster", "update", "-y", "--servers", "2", "non-existent-cluster")
			Expect(err).To(HaveOccurred())
			Expect(stderr).To(ContainSubstring("failed to fetch existing cluster"))
		})

		It("fails with invalid server count", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			// No cleanup needed - cluster is never created due to invalid input
			// Attempt to update with invalid server count
			_, stderr, err = K3kcli("cluster", "update", "-y", "--servers", "0", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(HaveOccurred())
			Expect(stderr).To(ContainSubstring("invalid number of servers"))
		})

		It("can update a cluster's labels", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			DeferCleanup(func() {
				DeleteNamespaces(clusterNamespace)
			})

			// Create the cluster first
			_, stderr, err = K3kcli("cluster", "create", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			// Update the cluster with labels
			_, stderr, err = K3kcli("cluster", "update", "-y", "--labels", "env=test", "--labels", "team=dev", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("Updating cluster"))

			// Verify the cluster labels were actually updated
			var cluster v1beta1.Cluster
			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster)
			Expect(err).To(Not(HaveOccurred()))
			Expect(cluster.Labels).To(HaveKeyWithValue("env", "test"))
			Expect(cluster.Labels).To(HaveKeyWithValue("team", "dev"))
		})

		It("can update a cluster's annotations", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			DeferCleanup(func() {
				DeleteNamespaces(clusterNamespace)
			})

			// Create the cluster first
			_, stderr, err = K3kcli("cluster", "create", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			// Update the cluster with annotations
			_, stderr, err = K3kcli("cluster", "update", "-y", "--annotations", "description=test-cluster", "--annotations", "owner=qa-team", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("Updating cluster"))

			// Verify the cluster annotations were actually updated
			var cluster v1beta1.Cluster
			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster)
			Expect(err).To(Not(HaveOccurred()))
			Expect(cluster.Annotations).To(HaveKeyWithValue("description", "test-cluster"))
			Expect(cluster.Annotations).To(HaveKeyWithValue("owner", "qa-team"))
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
