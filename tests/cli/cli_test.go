package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	corev1 "k8s.io/api/core/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/policy"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// deprecationWarning is the warning printed when a standalone kubeconfig is written to the
// current directory without being asked for.
const deprecationWarning = "Writing a kubeconfig to the current directory is deprecated"

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
	// the commands must only ever touch the copy of the kubeconfig made by the suite
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// hostKubeconfig loads the kubeconfig shared by the commands of the suite.
func hostKubeconfig() *clientcmdapi.Config {
	GinkgoHelper()

	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	Expect(err).To(Not(HaveOccurred()))

	return config
}

// checkCluster connects to a virtual cluster with a standalone kubeconfig file.
func checkCluster(path string) {
	GinkgoHelper()

	data, err := os.ReadFile(path)
	Expect(err).To(Not(HaveOccurred()))

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(data)
	Expect(err).To(Not(HaveOccurred()))

	checkServerVersion(restCfg)
}

// checkContext connects to a virtual cluster through the context added to the kubeconfig of
// the suite.
//
// The context is selected with an override rather than by reading the file directly: the
// current context still points at the host cluster, so a plain RESTConfigFromKubeConfig would
// silently connect to the host and pass for the wrong reason.
func checkContext(name string) {
	GinkgoHelper()

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{CurrentContext: name},
	)

	restCfg, err := clientConfig.ClientConfig()
	Expect(err).To(Not(HaveOccurred()))

	checkServerVersion(restCfg)
}

func checkServerVersion(restCfg *rest.Config) {
	GinkgoHelper()

	cs, err := kubernetes.NewForConfig(restCfg)
	Expect(err).To(Not(HaveOccurred()))

	Eventually(func() error {
		_, err := cs.Discovery().ServerVersion()
		return err
	}).
		WithTimeout(time.Minute).
		WithPolling(time.Second * 5).
		Should(Succeed())
}

var _ = When("using the k3kcli", Label("cli"), func() {
	It("can get the version", func() {
		stdout, _, err := K3kcli("--version")
		Expect(err).To(Not(HaveOccurred()))
		Expect(stdout).To(ContainSubstring("k3kcli version "))
	})

	When("trying the cluster commands", func() {
		It("can create, list and delete a cluster", func() {
			var (
				stdout string
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, namespace.Name)
			})

			contextName := clusterNamespace + "/" + clusterName
			hostContext := hostKubeconfig().CurrentContext

			By("Creating the cluster")

			_, stderr, err = K3kcli("cluster", "create", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))
			Expect(stderr).To(ContainSubstring("kubectl config use-context %s", contextName))
			Expect(stderr).To(ContainSubstring(deprecationWarning))

			By("Connecting to the cluster with the generated kubeconfig")

			cwd, err := os.Getwd()
			Expect(err).To(Not(HaveOccurred()))

			kubeconfig := filepath.Join(cwd, fmt.Sprintf("%s-%s-kubeconfig.yaml", clusterNamespace, clusterName))

			DeferCleanup(func() {
				_ = os.Remove(kubeconfig)
			})

			checkCluster(kubeconfig)

			By("Connecting to the cluster with the context added to the kubeconfig")

			config := hostKubeconfig()
			Expect(config.Contexts).To(HaveKey(contextName))

			// the kubeconfig of the user must keep working exactly as before
			Expect(config.Contexts).To(HaveKey(hostContext))
			Expect(config.CurrentContext).To(Equal(hostContext))

			checkContext(contextName)

			By("Listing the clusters")

			stdout, stderr, err = K3kcli("cluster", "list")
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(BeEmpty())
			Expect(stdout).To(ContainSubstring(clusterNamespace))

			By("Deleting the cluster")

			_, stderr, err = K3kcli("cluster", "delete", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Deleting '%s' cluster in namespace '%s'`, clusterName, clusterNamespace))

			By("Checking that the context was removed from the kubeconfig")

			config = hostKubeconfig()
			Expect(config.Contexts).To(Not(HaveKey(contextName)))
			Expect(config.Clusters).To(Not(HaveKey(contextName)))
			Expect(config.AuthInfos).To(Not(HaveKey(contextName)))

			Expect(config.Contexts).To(HaveKey(hostContext))
			Expect(config.CurrentContext).To(Equal(hostContext))

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
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, clusterNamespace)
			})

			By("Creating the cluster")

			_, stderr, err = K3kcli("cluster", "create", "--version", k3sVersion, "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))
		})

		It("can create a cluster with multiple --tls-sans values", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, clusterNamespace)
			})

			_, stderr, err = K3kcli("cluster", "create",
				"--namespace", clusterNamespace,
				"--tls-sans", "extra.example.com",
				"--tls-sans", "192.0.2.1",
				"--tls-sans", "host.example.com:6443",
				"--tls-sans", "2001:db8::1",
				"--tls-sans", "[2001:db8::1]:6443",
				clusterName,
			)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			var cluster v1beta1.Cluster

			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster)
			Expect(err).To(Not(HaveOccurred()))
			Expect(cluster.Spec.TLSSANs).To(ContainElements(
				"extra.example.com",
				"192.0.2.1",
				"host.example.com:6443",
				"2001:db8::1",
				"[2001:db8::1]:6443",
			))
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

			By("Creating a policy")

			_, stderr, err = K3kcli("policy", "create", policyName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Creating policy '%s'`, policyName))

			By("Listing the policies")

			stdout, stderr, err = K3kcli("policy", "list")
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(BeEmpty())
			Expect(stdout).To(ContainSubstring(policyName))

			By("Deleting the policy")

			stdout, stderr, err = K3kcli("policy", "delete", policyName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stdout).To(BeEmpty())
			Expect(stderr).To(ContainSubstring(`Policy '%s' deleted`, policyName))

			Eventually(func(g Gomega) {
				stdout, stderr, err = K3kcli("policy", "list")
				g.Expect(err).To(Not(HaveOccurred()), string(stderr))
				g.Expect(stdout).To(Not(ContainSubstring(policyName)))
			}).
				WithTimeout(time.Second * 5).
				WithPolling(time.Second).
				Should(Succeed())
		})

		It("can bound a policy to a namespace", func() {
			var (
				stdout string
				stderr string
				err    error
			)

			namespace := fwk3k.CreateNamespace(k8s)
			namespaceName := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, namespaceName)
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

			var ns corev1.Namespace

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
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, clusterNamespace)
			})

			By("Creating the cluster")

			// Create the cluster first
			_, stderr, err = K3kcli("cluster", "create", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			By("updating the cluster")

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
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, clusterNamespace)
			})

			By("Creating the cluster")

			// Create the cluster with initial version
			_, stderr, err = K3kcli("cluster", "create", "--version", k3sOldVersion, "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			By("updating the cluster")

			// Update the cluster version
			_, stderr, err = K3kcli("cluster", "update", "-y", "--version", k3sVersion, "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("Updating cluster"))

			// Verify the cluster state was actually updated
			var cluster v1beta1.Cluster

			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster)
			Expect(err).To(Not(HaveOccurred()))
			Expect(cluster.Spec.Version).To(Equal(k3sVersion))
		})

		It("fails to downgrade cluster version", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, clusterNamespace)
			})

			By("Creating the cluster")

			// Create the cluster with a version
			_, stderr, err = K3kcli("cluster", "create", "--version", k3sVersion, "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			By("Updating the cluster")

			// Attempt to downgrade should fail
			_, stderr, err = K3kcli("cluster", "update", "-y", "--version", k3sOldVersion, "--namespace", clusterNamespace, clusterName)
			Expect(err).To(HaveOccurred())
			Expect(stderr).To(ContainSubstring("downgrading cluster version is not supported"))

			// Verify the cluster version was NOT changed
			var cluster v1beta1.Cluster

			err = k8sClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster)
			Expect(err).To(Not(HaveOccurred()))
			Expect(cluster.Spec.Version).To(Equal(k3sVersion))
		})

		It("fails to update a non-existent cluster", func() {
			var (
				stderr string
				err    error
			)

			// Attempt to update a cluster that doesn't exist
			_, stderr, err = K3kcli("cluster", "update", "-y", "--servers", "2", "non-existent-cluster")
			Expect(err).To(HaveOccurred())
			Expect(stderr).To(ContainSubstring("failed to fetch cluster"))
		})

		It("can update a cluster's labels", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, clusterNamespace)
			})

			By("Creating the cluster")

			// Create the cluster first
			_, stderr, err = K3kcli("cluster", "create", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			By("Updating the cluster")

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
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, clusterNamespace)
			})

			By("Creating the cluster")

			// Create the cluster first
			_, stderr, err = K3kcli("cluster", "create", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			By("Updating the cluster")

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
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, clusterNamespace)
			})

			cwd, err := os.Getwd()
			Expect(err).To(Not(HaveOccurred()))

			kubeconfig := filepath.Join(cwd, fmt.Sprintf("%s-%s-kubeconfig.yaml", clusterNamespace, clusterName))

			DeferCleanup(func() {
				_ = os.Remove(kubeconfig)
			})

			By("Creating the cluster")

			_, stderr, err = K3kcli("cluster", "create", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			By("Connecting with the kubeconfig written by cluster create")

			checkCluster(kubeconfig)

			By("Generating the kubeconfig")

			_, stderr, err = K3kcli("kubeconfig", "generate", clusterNamespace+"/"+clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			By("Connecting with the kubeconfig written by kubeconfig generate")

			checkCluster(kubeconfig)

			By("Connecting with the context added by kubeconfig generate")

			checkContext(clusterNamespace + "/" + clusterName)

			_, stderr, err = K3kcli("cluster", "delete", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(`Deleting '%s' cluster in namespace '%s'`, clusterName, clusterNamespace))
		})

		It("can control the standalone kubeconfig with --out and --no-out", func() {
			var (
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			namespace := fwk3k.CreateNamespace(k8s)
			clusterNamespace := namespace.Name

			DeferCleanup(func() {
				fwk3k.DeleteNamespaces(k8s, clusterNamespace)
			})

			contextName := clusterNamespace + "/" + clusterName

			cwd, err := os.Getwd()
			Expect(err).To(Not(HaveOccurred()))

			legacyPath := filepath.Join(cwd, fmt.Sprintf("%s-%s-kubeconfig.yaml", clusterNamespace, clusterName))

			DeferCleanup(func() {
				_ = os.Remove(legacyPath)
			})

			By("Creating the cluster with --no-out")

			_, stderr, err = K3kcli("cluster", "create", "--no-out", "--namespace", clusterNamespace, clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(Not(ContainSubstring(deprecationWarning)))
			Expect(legacyPath).To(Not(BeAnExistingFile()))

			By("Connecting with the context, which is added even without a standalone kubeconfig")

			Expect(hostKubeconfig().Contexts).To(HaveKey(contextName))

			checkContext(contextName)

			By("Generating a standalone kubeconfig at the path given to --out")

			outPath := filepath.Join(GinkgoT().TempDir(), "out.yaml")

			_, stderr, err = K3kcli("kubeconfig", "generate", "--out", outPath, "--namespace", clusterNamespace, "--name", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(Not(ContainSubstring(deprecationWarning)))
			Expect(legacyPath).To(Not(BeAnExistingFile()))

			checkCluster(outPath)
		})
	})
})
