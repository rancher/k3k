package upgrade_test

// The cluster helpers below are a copy of the ones in tests/e2e/common_test.go,
// and runCmd is a copy of the one in tests/cli/cli_test.go. The duplication is
// deliberate: sharing them means moving them into tests/framework, which is a
// refactor of the e2e and cli suites and does not belong in this change. Only
// what this suite actually uses was copied.

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// runCmd executes cmdName with args and returns its stdout, stderr and error.
func runCmd(cmdName string, args ...string) (string, string, error) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}

	cmd := exec.CommandContext(context.Background(), cmdName, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// NewCluster returns a Cluster spec in the given namespace, exposed with a NodePort
// and reachable from the host. The options are applied in order, and can override
// any of the defaults.
func NewCluster(namespace string, opts ...func(*v1beta1.Cluster)) *v1beta1.Cluster {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "cluster-",
			Namespace:    namespace,
		},
		Spec: v1beta1.ClusterSpec{
			TLSSANs: []string{hostIP},
			Expose: &v1beta1.ExposeConfig{
				NodePort: &v1beta1.NodePortConfig{},
			},
			Persistence: v1beta1.PersistenceConfig{
				Type: v1beta1.EphemeralPersistenceMode,
			},
		},
	}

	for _, optFn := range opts {
		optFn(cluster)
	}

	return cluster
}

// CreateCluster creates the Cluster and waits for its server and agent pods to be Ready.
func CreateCluster(ctx context.Context, cluster *v1beta1.Cluster) {
	GinkgoHelper()

	By(fmt.Sprintf("Creating new virtual cluster in namespace %s", cluster.Namespace))

	err := k8sClient.Create(ctx, cluster)
	Expect(err).To(Not(HaveOccurred()))

	// The servers/agents defaults come from the CRD, but they are not guaranteed to
	// be set: this suite runs against the CRDs of an older, released k3k.
	expectedServers := int(ptr.Deref(cluster.Spec.Servers, 1))
	expectedAgents := int(ptr.Deref(cluster.Spec.Agents, 0))

	By(fmt.Sprintf("Waiting for cluster %s to be ready in namespace %s. Expected servers: %d. Expected agents: %d", cluster.Name, cluster.Namespace, expectedServers, expectedAgents))

	// track the Eventually status to log for changes
	prev := -1

	// check that the server Pod and the Kubelet are in Ready state
	Eventually(func() bool {
		podList, err := k8s.CoreV1().Pods(cluster.Namespace).List(ctx, metav1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))

		// all the servers and agents needs to be in a running phase
		var serversReady, agentsReady int

		for _, k3sPod := range podList.Items {
			if k3sPod.Labels["cluster"] != cluster.Name {
				continue
			}

			_, cond := pod.GetPodCondition(&k3sPod.Status, corev1.PodReady)

			// pod not ready
			if cond == nil || cond.Status != corev1.ConditionTrue {
				continue
			}

			if k3sPod.Labels["role"] == "server" {
				serversReady++
			}

			if k3sPod.Labels["type"] == "agent" {
				agentsReady++
			}
		}

		if prev != (serversReady + agentsReady) {
			GinkgoLogr.Info("Waiting for pods to be Ready",
				"servers", serversReady, "agents", agentsReady,
				"name", cluster.Name, "namespace", cluster.Namespace,
				"time", time.Now().Format(time.DateTime),
			)
			prev = (serversReady + agentsReady)
		}

		// the server pods should equal the expected servers, but since in shared mode we also have the kubelet is fine to have more than one
		if (serversReady != expectedServers) || (agentsReady < expectedAgents) {
			return false
		}

		return true
	}).
		WithTimeout(time.Minute * 5).
		WithPolling(time.Second * 10).
		Should(BeTrue())

	By("Cluster is ready")
}

// newVirtualK8sClient returns a Kubernetes ClientSet for the virtual cluster.
func newVirtualK8sClient(ctx context.Context, cluster *v1beta1.Cluster) *kubernetes.Clientset {
	GinkgoHelper()

	var (
		err    error
		config *clientcmdapi.Config
	)

	Eventually(func() error {
		vKubeconfig := kubeconfig.New()
		kubeletAltName := fmt.Sprintf("k3k-%s-kubelet", cluster.Name)
		vKubeconfig.AltNames = certs.AddSANs([]string{hostIP, kubeletAltName})
		config, err = vKubeconfig.Generate(ctx, k8sClient, cluster, hostIP)

		return err
	}).
		WithTimeout(time.Minute * 2).
		WithPolling(time.Second * 5).
		Should(BeNil())

	configData, err := clientcmd.Write(*config)
	Expect(err).To(Not(HaveOccurred()))

	restcfg, err := clientcmd.RESTConfigFromKubeConfig(configData)
	Expect(err).To(Not(HaveOccurred()))
	virtualK8sClient, err := kubernetes.NewForConfig(restcfg)
	Expect(err).To(Not(HaveOccurred()))

	return virtualK8sClient
}

// listServerPods returns the server pods of the cluster, from the host cluster.
func listServerPods(ctx context.Context, cluster *v1beta1.Cluster) []corev1.Pod {
	GinkgoHelper()

	labelSelector := "cluster=" + cluster.Name + ",role=server"

	serverPods, err := k8s.CoreV1().Pods(cluster.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	Expect(err).To(Not(HaveOccurred()))

	return serverPods.Items
}
