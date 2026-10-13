package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// An HCP cluster owns no agents: the workers live outside the host cluster and reach
// the servers through the NodePort. Streaming commands (logs, exec) are proxied from
// whichever server handles the request back down the worker's tunnel, so every server
// needs its own tunnel to that worker. When only one of them is individually
// reachable, the requests that land on the others time out with a 502.
//
// See https://github.com/rancher/k3k/issues/1002
var _ = When("an external worker joins an HCP cluster", Label(hcpTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		skipWithoutDocker()
		skipWithSingleHostNode()

		namespace := fwk3k.CreateNamespace(k8s)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
		})

		cluster := NewCluster(namespace.Name)
		cluster.Spec.Mode = v1beta1.HCPClusterMode
		cluster.Spec.Servers = new(int32(3))
		// Spread the servers so that they end up behind distinct node IPs.
		// Preferred, not required, to keep the cluster schedulable on smaller hosts.
		cluster.Spec.ServerAffinity = spreadServersAffinity()

		CreateCluster(cluster)

		virtualK8s, restCfg := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restCfg,
			Client:     virtualK8s,
		}

		joinK3sWorker(cluster, restCfg.Host, clusterToken(cluster))
	})

	It("can read the logs of a pod through every server", func() {
		ctx := GinkgoT().Context()

		By("Waiting for the external worker to register and become Ready")

		Eventually(func(g Gomega) {
			nodes, err := virtualCluster.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(nodes.Items).To(HaveLen(1))
			g.Expect(nodeReady(nodes.Items[0])).To(BeTrue())
		}).
			WithTimeout(time.Minute * 3).
			WithPolling(time.Second * 5).
			Should(Succeed())

		By("Running a pod on the external worker")

		loggingPod, marker := runLoggingPod(ctx, virtualCluster)

		// One endpoint per node running a server. Reading the logs through each of
		// them is what distinguishes a cluster where every server can serve a stream
		// from one where only the server holding the tunnel can.
		servers := kubernetesEndpoints(ctx, virtualCluster)
		Expect(len(servers)).To(BeNumerically(">=", 2))

		for _, server := range servers {
			By("Reading the logs through the server on " + server)

			serverClient := clientForServer(virtualCluster.RestConfig, server)

			Eventually(func(g Gomega) {
				logs, err := podLogs(ctx, serverClient, loggingPod)
				g.Expect(err).To(Not(HaveOccurred()))
				g.Expect(logs).To(ContainSubstring(marker))
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second * 5).
				Should(Succeed())
		}
	})
})

// skipWithoutDocker skips the spec when no usable Docker daemon is around, so the
// suite still runs on hosts that cannot start the worker container.
func skipWithoutDocker() {
	GinkgoHelper()

	if err := exec.Command("docker", "info").Run(); err != nil {
		Skip("skipping: no usable docker daemon (" + err.Error() + ")")
	}
}

// skipWithSingleHostNode skips the spec on a single node host cluster, where all the
// servers share one node IP and the multi-tunnel path cannot be exercised.
func skipWithSingleHostNode() {
	GinkgoHelper()

	nodes, err := k8s.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	Expect(err).To(Not(HaveOccurred()))

	if len(nodes.Items) < 2 {
		Skip("skipping: the host cluster has a single node")
	}
}

// spreadServersAffinity keeps the server pods of a cluster on separate nodes when the
// host cluster has the room for it.
func spreadServersAffinity() *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					TopologyKey:   corev1.LabelHostname,
					LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "server"}},
				},
			}},
		},
	}
}

// joinK3sWorker starts a k3s agent in a container and joins it to the virtual cluster
// at serverURL. The container is torn down, and its logs dumped, when the spec ends.
func joinK3sWorker(cluster *v1beta1.Cluster, serverURL, token string) {
	GinkgoHelper()

	name := "k3k-e2e-worker-" + cluster.Name

	By("Starting the k3s agent container " + name + " joining " + serverURL)

	out, err := exec.Command("docker", "run",
		"--detach",
		"--privileged",
		"--name", name,
		"--hostname", name,
		"--env", "K3S_URL="+serverURL,
		"--env", "K3S_TOKEN="+token,
		"rancher/k3s:"+k3sVersion,
		"agent",
	).CombinedOutput()
	Expect(err).To(Not(HaveOccurred()), "docker run failed: %s", out)

	DeferCleanup(func() {
		logs, _ := exec.Command("docker", "logs", "--tail", "100", name).CombinedOutput()
		GinkgoWriter.Printf("k3s agent %s logs:\n%s\n", name, logs)

		if out, err := exec.Command("docker", "rm", "--force", "--volumes", name).CombinedOutput(); err != nil {
			GinkgoWriter.Printf("could not remove the k3s agent %s: %s\n", name, out)
		}
	})
}

// clusterToken returns the join token of the given cluster.
func clusterToken(cluster *v1beta1.Cluster) string {
	GinkgoHelper()

	var tokenSecret corev1.Secret

	key := client.ObjectKey{
		Name:      k3kcluster.TokenSecretName(cluster.Name),
		Namespace: cluster.Namespace,
	}

	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(context.Background(), key, &tokenSecret)).To(Succeed())
		g.Expect(tokenSecret.Data["token"]).To(Not(BeEmpty()))
	}).
		WithTimeout(time.Minute).
		WithPolling(time.Second).
		Should(Succeed())

	return string(tokenSecret.Data["token"])
}

// kubernetesEndpoints returns the "host:port" of every endpoint the virtual cluster
// publishes for the default/kubernetes Service, i.e. one entry per reachable server.
func kubernetesEndpoints(ctx context.Context, virtualCluster *VirtualCluster) []string {
	GinkgoHelper()

	var addresses []string

	Eventually(func(g Gomega) {
		addresses = nil

		slices, err := virtualCluster.Client.DiscoveryV1().EndpointSlices(metav1.NamespaceDefault).
			List(ctx, metav1.ListOptions{LabelSelector: discoveryv1.LabelServiceName + "=kubernetes"})
		g.Expect(err).To(Not(HaveOccurred()))

		for _, slice := range slices.Items {
			for _, port := range slice.Ports {
				for _, endpoint := range slice.Endpoints {
					for _, address := range endpoint.Addresses {
						addresses = append(addresses, net.JoinHostPort(address, strconv.Itoa(int(*port.Port))))
					}
				}
			}
		}

		g.Expect(addresses).To(Not(BeEmpty()))
	}).
		WithTimeout(time.Minute).
		WithPolling(time.Second * 5).
		Should(Succeed())

	return addresses
}

// clientForServer returns a client talking to one specific server of the virtual
// cluster, bypassing the load balancing the NodePort would otherwise do.
func clientForServer(restCfg *rest.Config, server string) *kubernetes.Clientset {
	GinkgoHelper()

	serverCfg := rest.CopyConfig(restCfg)
	serverCfg.Host = "https://" + server

	serverClient, err := kubernetes.NewForConfig(serverCfg)
	Expect(err).To(Not(HaveOccurred()))

	return serverClient
}

// runLoggingPod schedules a pod writing a unique marker to its stdout, and returns it
// once running together with that marker.
func runLoggingPod(ctx context.Context, virtualCluster *VirtualCluster) (*corev1.Pod, string) {
	GinkgoHelper()

	marker := fmt.Sprintf("hello-from-the-worker-%d", time.Now().UnixNano())

	loggingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "logger-",
			Namespace:    metav1.NamespaceDefault,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "logger",
				Image:   "busybox",
				Command: []string{"sh", "-c", "echo " + marker + "; sleep 3600"},
			}},
		},
	}

	loggingPod, err := virtualCluster.Client.CoreV1().Pods(loggingPod.Namespace).
		Create(ctx, loggingPod, metav1.CreateOptions{})
	Expect(err).To(Not(HaveOccurred()))

	Eventually(func(g Gomega) {
		loggingPod, err = virtualCluster.Client.CoreV1().Pods(loggingPod.Namespace).
			Get(ctx, loggingPod.Name, metav1.GetOptions{})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(loggingPod.Status.Phase).To(Equal(corev1.PodRunning))
	}).
		WithTimeout(time.Minute * 2).
		WithPolling(time.Second * 5).
		Should(Succeed())

	return loggingPod, marker
}

// podLogs reads the logs of the given pod through the given client.
func podLogs(ctx context.Context, k8sClient *kubernetes.Clientset, loggingPod *corev1.Pod) (string, error) {
	stream, err := k8sClient.CoreV1().Pods(loggingPod.Namespace).
		GetLogs(loggingPod.Name, &corev1.PodLogOptions{}).Stream(ctx)
	if err != nil {
		return "", err
	}

	defer func() {
		_ = stream.Close()
	}()

	logs, err := io.ReadAll(stream)

	return string(logs), err
}

// nodeReady reports whether the given node is Ready.
func nodeReady(node corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}

	return false
}
