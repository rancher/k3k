package k3k_test

import (
	"context"
	"strings"
	"time"

	"k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/ptr"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a shared mode cluster update its envs", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster
	ctx := context.Background()
	BeforeEach(func() {
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		// Add initial environment variables for server
		cluster.Spec.ServerEnvs = []v1.EnvVar{
			{
				Name:  "TEST_SERVER_ENV_1",
				Value: "not_upgraded",
			},
			{
				Name:  "TEST_SERVER_ENV_2",
				Value: "toBeRemoved",
			},
		}
		// Add initial environment variables for agent
		cluster.Spec.AgentEnvs = []v1.EnvVar{
			{
				Name:  "TEST_AGENT_ENV_1",
				Value: "not_upgraded",
			},
			{
				Name:  "TEST_AGENT_ENV_2",
				Value: "toBeRemoved",
			},
		}

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(1))

		serverPod := sPods[0]

		serverEnv1, ok := getEnv(&serverPod, "TEST_SERVER_ENV_1")
		Expect(ok).To(BeTrue())
		Expect(serverEnv1).To(Equal("not_upgraded"))

		serverEnv2, ok := getEnv(&serverPod, "TEST_SERVER_ENV_2")
		Expect(ok).To(BeTrue())
		Expect(serverEnv2).To(Equal("toBeRemoved"))

		aPods := listAgentPods(ctx, virtualCluster)
		Expect(len(aPods)).To(Equal(1))

		agentPod := aPods[0]

		agentEnv1, ok := getEnv(&agentPod, "TEST_AGENT_ENV_1")
		Expect(ok).To(BeTrue())
		Expect(agentEnv1).To(Equal("not_upgraded"))

		agentEnv2, ok := getEnv(&agentPod, "TEST_AGENT_ENV_2")
		Expect(ok).To(BeTrue())
		Expect(agentEnv2).To(Equal("toBeRemoved"))
	})
	It("will update server and agent envs when cluster is updated", func() {
		Eventually(func(g Gomega) {
			var cluster v1beta1.Cluster

			err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
			g.Expect(err).NotTo(HaveOccurred())

			// update both agent and server envs
			cluster.Spec.ServerEnvs = []v1.EnvVar{
				{
					Name:  "TEST_SERVER_ENV_1",
					Value: "upgraded",
				},
				{
					Name:  "TEST_SERVER_ENV_3",
					Value: "new",
				},
			}
			cluster.Spec.AgentEnvs = []v1.EnvVar{
				{
					Name:  "TEST_AGENT_ENV_1",
					Value: "upgraded",
				},
				{
					Name:  "TEST_AGENT_ENV_3",
					Value: "new",
				},
			}

			err = k8sClient.Update(ctx, &cluster)
			g.Expect(err).NotTo(HaveOccurred())

			// server pods
			serverPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(serverPods)).To(Equal(1))

			serverEnv1, ok := getEnv(&serverPods[0], "TEST_SERVER_ENV_1")
			g.Expect(ok).To(BeTrue())
			g.Expect(serverEnv1).To(Equal("upgraded"))

			_, ok = getEnv(&serverPods[0], "TEST_SERVER_ENV_2")
			g.Expect(ok).To(BeFalse())

			serverEnv3, ok := getEnv(&serverPods[0], "TEST_SERVER_ENV_3")
			g.Expect(ok).To(BeTrue())
			g.Expect(serverEnv3).To(Equal("new"))

			// agent pods
			aPods := listAgentPods(ctx, virtualCluster)
			g.Expect(len(aPods)).To(Equal(1))

			agentEnv1, ok := getEnv(&aPods[0], "TEST_AGENT_ENV_1")
			g.Expect(ok).To(BeTrue())
			g.Expect(agentEnv1).To(Equal("upgraded"))

			_, ok = getEnv(&aPods[0], "TEST_AGENT_ENV_2")
			g.Expect(ok).To(BeFalse())

			agentEnv3, ok := getEnv(&aPods[0], "TEST_AGENT_ENV_3")
			g.Expect(ok).To(BeTrue())
			g.Expect(agentEnv3).To(Equal("new"))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 2).
			Should(Succeed())
	})
})

var _ = When("a shared mode cluster update its server args", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster
	ctx := context.Background()
	BeforeEach(func() {
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		// Add initial args for server
		cluster.Spec.ServerArgs = []string{
			"--node-label=test_server=not_upgraded",
		}

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(1))

		serverPod := sPods[0]

		Expect(isArgFound(&serverPod, "--node-label=test_server=not_upgraded")).To(BeTrue())
	})
	It("will update server args", func() {
		Eventually(func(g Gomega) {
			var cluster v1beta1.Cluster

			err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
			g.Expect(err).NotTo(HaveOccurred())

			cluster.Spec.ServerArgs = []string{
				"--node-label=test_server=upgraded",
			}

			err = k8sClient.Update(ctx, &cluster)
			g.Expect(err).NotTo(HaveOccurred())

			// server pods
			sPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(sPods)).To(Equal(1))

			g.Expect(isArgFound(&sPods[0], "--node-label=test_server=upgraded")).To(BeTrue())
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 2).
			Should(Succeed())
	})
})

var _ = When("a virtual mode cluster update its envs", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster
	ctx := context.Background()
	BeforeEach(func() {
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		// Add initial environment variables for server
		cluster.Spec.ServerEnvs = []v1.EnvVar{
			{
				Name:  "TEST_SERVER_ENV_1",
				Value: "not_upgraded",
			},
			{
				Name:  "TEST_SERVER_ENV_2",
				Value: "toBeRemoved",
			},
		}
		// Add initial environment variables for agent
		cluster.Spec.AgentEnvs = []v1.EnvVar{
			{
				Name:  "TEST_AGENT_ENV_1",
				Value: "not_upgraded",
			},
			{
				Name:  "TEST_AGENT_ENV_2",
				Value: "toBeRemoved",
			},
		}

		cluster.Spec.Mode = v1beta1.VirtualClusterMode
		cluster.Spec.Agents = ptr.To[int32](1)

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(1))

		serverPod := sPods[0]

		serverEnv1, ok := getEnv(&serverPod, "TEST_SERVER_ENV_1")
		Expect(ok).To(BeTrue())
		Expect(serverEnv1).To(Equal("not_upgraded"))

		serverEnv2, ok := getEnv(&serverPod, "TEST_SERVER_ENV_2")
		Expect(ok).To(BeTrue())
		Expect(serverEnv2).To(Equal("toBeRemoved"))

		aPods := listAgentPods(ctx, virtualCluster)
		Expect(len(aPods)).To(Equal(1))

		agentPod := aPods[0]

		agentEnv1, ok := getEnv(&agentPod, "TEST_AGENT_ENV_1")
		Expect(ok).To(BeTrue())
		Expect(agentEnv1).To(Equal("not_upgraded"))

		agentEnv2, ok := getEnv(&agentPod, "TEST_AGENT_ENV_2")
		Expect(ok).To(BeTrue())
		Expect(agentEnv2).To(Equal("toBeRemoved"))
	})
	It("will update server and agent envs when cluster is updated", func() {
		Eventually(func(g Gomega) {
			var cluster v1beta1.Cluster

			err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
			g.Expect(err).NotTo(HaveOccurred())

			// update both agent and server envs
			cluster.Spec.ServerEnvs = []v1.EnvVar{
				{
					Name:  "TEST_SERVER_ENV_1",
					Value: "upgraded",
				},
				{
					Name:  "TEST_SERVER_ENV_3",
					Value: "new",
				},
			}
			cluster.Spec.AgentEnvs = []v1.EnvVar{
				{
					Name:  "TEST_AGENT_ENV_1",
					Value: "upgraded",
				},
				{
					Name:  "TEST_AGENT_ENV_3",
					Value: "new",
				},
			}

			err = k8sClient.Update(ctx, &cluster)
			g.Expect(err).NotTo(HaveOccurred())

			// server pods
			serverPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(serverPods)).To(Equal(1))

			serverEnv1, ok := getEnv(&serverPods[0], "TEST_SERVER_ENV_1")
			g.Expect(ok).To(BeTrue())
			g.Expect(serverEnv1).To(Equal("upgraded"))

			_, ok = getEnv(&serverPods[0], "TEST_SERVER_ENV_2")
			g.Expect(ok).To(BeFalse())

			serverEnv3, ok := getEnv(&serverPods[0], "TEST_SERVER_ENV_3")
			g.Expect(ok).To(BeTrue())
			g.Expect(serverEnv3).To(Equal("new"))

			// agent pods
			aPods := listAgentPods(ctx, virtualCluster)
			g.Expect(len(aPods)).To(Equal(1))

			agentEnv1, ok := getEnv(&aPods[0], "TEST_AGENT_ENV_1")
			g.Expect(ok).To(BeTrue())
			g.Expect(agentEnv1).To(Equal("upgraded"))

			_, ok = getEnv(&aPods[0], "TEST_AGENT_ENV_2")
			g.Expect(ok).To(BeFalse())

			agentEnv3, ok := getEnv(&aPods[0], "TEST_AGENT_ENV_3")
			g.Expect(ok).To(BeTrue())
			g.Expect(agentEnv3).To(Equal("new"))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 2).
			Should(Succeed())
	})
})

var _ = When("a virtual mode cluster update its server args", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster
	ctx := context.Background()
	BeforeEach(func() {
		namespace := NewNamespace()

		cluster := NewCluster(namespace.Name)

		// Add initial args for server
		cluster.Spec.ServerArgs = []string{
			"--node-label=test_server=not_upgraded",
		}

		cluster.Spec.Mode = v1beta1.VirtualClusterMode
		cluster.Spec.Agents = ptr.To[int32](1)

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(1))

		serverPod := sPods[0]

		Expect(isArgFound(&serverPod, "--node-label=test_server=not_upgraded")).To(BeTrue())
	})
	It("will update server args", func() {
		Eventually(func(g Gomega) {
			var cluster v1beta1.Cluster

			err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
			g.Expect(err).NotTo(HaveOccurred())

			cluster.Spec.ServerArgs = []string{
				"--node-label=test_server=upgraded",
			}

			err = k8sClient.Update(ctx, &cluster)
			g.Expect(err).NotTo(HaveOccurred())

			// server pods
			sPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(sPods)).To(Equal(1))

			g.Expect(isArgFound(&sPods[0], "--node-label=test_server=upgraded")).To(BeTrue())
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 2).
			Should(Succeed())
	})
})

var _ = When("a shared mode cluster update its version", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var (
		virtualCluster *VirtualCluster
		nginxPod       *v1.Pod
	)
	BeforeEach(func() {
		ctx := context.Background()
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		// Add initial version
		cluster.Spec.Version = "v1.31.13-k3s1"

		// need to enable persistence for this
		cluster.Spec.Persistence = v1beta1.PersistenceConfig{
			Type: v1beta1.DynamicPersistenceMode,
		}

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(1))

		serverPod := sPods[0]
		Expect(serverPod.Spec.Containers[0].Image).To(Equal("rancher/k3s:" + cluster.Spec.Version))

		nginxPod, _ = virtualCluster.NewNginxPod("")

		DeferCleanup(func() {
			DeleteNamespaces(virtualCluster.Cluster.Namespace)
		})
	})

	It("will update server version when version spec is updated", func() {
		var cluster v1beta1.Cluster
		ctx := context.Background()

		err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
		Expect(err).NotTo(HaveOccurred())

		// update cluster version
		cluster.Spec.Version = "v1.32.8-k3s1"

		err = k8sClient.Update(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			// server pods
			serverPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(serverPods)).To(Equal(1))

			serverPod := serverPods[0]
			_, cond := pod.GetPodCondition(&serverPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))

			g.Expect(serverPod.Spec.Containers[0].Image).To(Equal("rancher/k3s:" + cluster.Spec.Version))

			clusterVersion, err := virtualCluster.Client.Discovery().ServerVersion()
			g.Expect(err).To(BeNil())
			g.Expect(clusterVersion.String()).To(Equal(strings.ReplaceAll(cluster.Spec.Version, "-", "+")))

			nginxPod, err = virtualCluster.Client.CoreV1().Pods(nginxPod.Namespace).Get(ctx, nginxPod.Name, metav1.GetOptions{})
			g.Expect(err).To(BeNil())
			_, cond = pod.GetPodCondition(&nginxPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}).
			WithPolling(time.Second * 5).
			WithTimeout(time.Minute * 3).
			Should(Succeed())
	})
})

var _ = When("a virtual mode cluster update its version", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var (
		virtualCluster *VirtualCluster
		nginxPod       *v1.Pod
	)
	BeforeEach(func() {
		ctx := context.Background()
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		// Add initial version
		cluster.Spec.Version = "v1.31.13-k3s1"

		cluster.Spec.Mode = v1beta1.VirtualClusterMode
		cluster.Spec.Agents = ptr.To[int32](1)

		// need to enable persistence for this
		cluster.Spec.Persistence = v1beta1.PersistenceConfig{
			Type: v1beta1.DynamicPersistenceMode,
		}

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(1))

		serverPod := sPods[0]
		Expect(serverPod.Spec.Containers[0].Image).To(Equal("rancher/k3s:" + cluster.Spec.Version))

		aPods := listAgentPods(ctx, virtualCluster)
		Expect(len(aPods)).To(Equal(1))

		agentPod := aPods[0]
		Expect(agentPod.Spec.Containers[0].Image).To(Equal("rancher/k3s:" + cluster.Spec.Version))

		nginxPod, _ = virtualCluster.NewNginxPod("")
	})
	It("will update server version when version spec is updated", func() {
		var cluster v1beta1.Cluster
		ctx := context.Background()

		err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
		Expect(err).NotTo(HaveOccurred())

		// update cluster version
		cluster.Spec.Version = "v1.32.8-k3s1"

		err = k8sClient.Update(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			// server pods
			serverPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(serverPods)).To(Equal(1))

			serverPod := serverPods[0]
			_, cond := pod.GetPodCondition(&serverPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))

			g.Expect(serverPod.Spec.Containers[0].Image).To(Equal("rancher/k3s:" + cluster.Spec.Version))

			// agent pods
			agentPods := listAgentPods(ctx, virtualCluster)
			g.Expect(len(agentPods)).To(Equal(1))

			agentPod := agentPods[0]
			_, cond = pod.GetPodCondition(&agentPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))

			g.Expect(agentPod.Spec.Containers[0].Image).To(Equal("rancher/k3s:" + cluster.Spec.Version))

			clusterVersion, err := virtualCluster.Client.Discovery().ServerVersion()
			g.Expect(err).To(BeNil())
			g.Expect(clusterVersion.String()).To(Equal(strings.ReplaceAll(cluster.Spec.Version, "-", "+")))

			nginxPod, err = virtualCluster.Client.CoreV1().Pods(nginxPod.Namespace).Get(ctx, nginxPod.Name, metav1.GetOptions{})
			g.Expect(err).To(BeNil())

			_, cond = pod.GetPodCondition(&nginxPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 3).
			Should(Succeed())
	})
})

var _ = When("a shared mode cluster scales up servers", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var (
		virtualCluster *VirtualCluster
		nginxPod       *v1.Pod
	)
	BeforeEach(func() {
		ctx := context.Background()
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		// need to enable persistence for this
		cluster.Spec.Persistence = v1beta1.PersistenceConfig{
			Type: v1beta1.DynamicPersistenceMode,
		}

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(1))

		Eventually(func(g Gomega) {
			// since there is no way to check nodes in shared mode
			// we can check if the endpoints are registered to N nodes
			k8sEndpointSlices, err := virtualCluster.Client.DiscoveryV1().EndpointSlices("default").Get(ctx, "kubernetes", metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(len(k8sEndpointSlices.Endpoints)).To(Equal(1))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 3).
			Should(Succeed())

		nginxPod, _ = virtualCluster.NewNginxPod("")
	})
	It("will scale up server pods", func() {
		var cluster v1beta1.Cluster
		ctx := context.Background()

		err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
		Expect(err).NotTo(HaveOccurred())

		// scale cluster servers to 3 nodes
		cluster.Spec.Servers = ptr.To[int32](3)

		err = k8sClient.Update(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			// server pods
			serverPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(serverPods)).To(Equal(3))

			for _, serverPod := range serverPods {
				_, cond := pod.GetPodCondition(&serverPod.Status, v1.PodReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
			}

			k8sEndpointSlices, err := virtualCluster.Client.DiscoveryV1().EndpointSlices("default").Get(ctx, "kubernetes", metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(len(k8sEndpointSlices.Endpoints)).To(Equal(3))

			nginxPod, err = virtualCluster.Client.CoreV1().Pods(nginxPod.Namespace).Get(ctx, nginxPod.Name, metav1.GetOptions{})
			g.Expect(err).To(BeNil())
			_, cond := pod.GetPodCondition(&nginxPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 3).
			Should(Succeed())
	})
})

var _ = When("a shared mode cluster scales down servers", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var (
		virtualCluster *VirtualCluster
		nginxPod       *v1.Pod
	)
	BeforeEach(func() {
		ctx := context.Background()
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		// start cluster with 3 servers
		cluster.Spec.Servers = ptr.To[int32](3)

		// need to enable persistence for this
		cluster.Spec.Persistence = v1beta1.PersistenceConfig{
			Type: v1beta1.DynamicPersistenceMode,
		}

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		// no need to check servers status since createCluster() will wait until all servers are in ready state
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(3))

		Eventually(func(g Gomega) {
			// since there is no way to check nodes in shared mode
			// we can check if the endpoints are registered to N nodes
			k8sEndpointSlices, err := virtualCluster.Client.DiscoveryV1().EndpointSlices("default").Get(ctx, "kubernetes", metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(len(k8sEndpointSlices.Endpoints)).To(Equal(3))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 3).
			Should(Succeed())

		nginxPod, _ = virtualCluster.NewNginxPod("")
	})
	It("will scale down server pods", func() {
		var cluster v1beta1.Cluster
		ctx := context.Background()

		err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
		Expect(err).NotTo(HaveOccurred())

		// scale down cluster servers to 1 node
		cluster.Spec.Servers = ptr.To[int32](1)

		err = k8sClient.Update(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			// server pods
			serverPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(serverPods)).To(Equal(1))

			_, cond := pod.GetPodCondition(&serverPods[0].Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))

			k8sEndpointSlices, err := virtualCluster.Client.DiscoveryV1().EndpointSlices("default").Get(ctx, "kubernetes", metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(len(k8sEndpointSlices.Endpoints)).To(Equal(1))

			nginxPod, err = virtualCluster.Client.CoreV1().Pods(nginxPod.Namespace).Get(ctx, nginxPod.Name, metav1.GetOptions{})
			g.Expect(err).To(BeNil())
			_, cond = pod.GetPodCondition(&nginxPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 3).
			Should(Succeed())
	})
})

var _ = When("a virtual mode cluster scales up servers", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var (
		virtualCluster *VirtualCluster
		nginxPod       *v1.Pod
	)
	BeforeEach(func() {
		ctx := context.Background()
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		cluster.Spec.Mode = v1beta1.VirtualClusterMode

		// need to enable persistence for this
		cluster.Spec.Persistence = v1beta1.PersistenceConfig{
			Type: v1beta1.DynamicPersistenceMode,
		}

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(1))

		Eventually(func(g Gomega) {
			nodes, err := virtualCluster.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(nodes.Items)).To(Equal(1))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 5).
			Should(Succeed())

		nginxPod, _ = virtualCluster.NewNginxPod("")
	})
	It("will scale up server pods", func() {
		var cluster v1beta1.Cluster
		ctx := context.Background()

		err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
		Expect(err).NotTo(HaveOccurred())

		// scale cluster servers to 3 nodes
		cluster.Spec.Servers = ptr.To[int32](3)

		err = k8sClient.Update(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			// server pods
			serverPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(serverPods)).To(Equal(3))

			for _, serverPod := range serverPods {
				_, cond := pod.GetPodCondition(&serverPod.Status, v1.PodReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
			}

			nodes, err := virtualCluster.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(nodes.Items)).To(Equal(3))

			nginxPod, err = virtualCluster.Client.CoreV1().Pods(nginxPod.Namespace).Get(ctx, nginxPod.Name, metav1.GetOptions{})
			g.Expect(err).To(BeNil())
			_, cond := pod.GetPodCondition(&nginxPod.Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 5).
			Should(Succeed())
	})
})

var _ = When("a virtual mode cluster scales down servers", Label("e2e"), Label(updateTestsLabel), Label(slowTestsLabel), func() {
	var (
		virtualCluster *VirtualCluster
		nginxPod       *v1.Pod
	)
	BeforeEach(func() {
		ctx := context.Background()
		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

		cluster := NewCluster(namespace.Name)

		cluster.Spec.Mode = v1beta1.VirtualClusterMode

		// start cluster with 3 servers
		cluster.Spec.Servers = ptr.To[int32](3)

		// need to enable persistence for this
		cluster.Spec.Persistence = v1beta1.PersistenceConfig{
			Type: v1beta1.DynamicPersistenceMode,
		}

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
		// no need to check servers status since createCluster() will wait until all servers are in ready state
		sPods := listServerPods(ctx, virtualCluster)
		Expect(len(sPods)).To(Equal(3))

		Eventually(func(g Gomega) {
			nodes, err := virtualCluster.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(nodes.Items)).To(Equal(3))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute * 5).
			Should(Succeed())

		nginxPod, _ = virtualCluster.NewNginxPod("")
	})

	It("will scale down server pods", func() {
		By("Scaling down cluster")

		var cluster v1beta1.Cluster
		ctx := context.Background()

		err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
		Expect(err).NotTo(HaveOccurred())

		// scale down cluster servers to 1 node
		cluster.Spec.Servers = ptr.To[int32](1)

		err = k8sClient.Update(ctx, &cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			serverPods := listServerPods(ctx, virtualCluster)

			// Wait for all the server pods to be marked for deletion
			for _, serverPod := range serverPods {
				g.Expect(serverPod.DeletionTimestamp).NotTo(BeNil())
			}
		}).
			MustPassRepeatedly(5).
			WithPolling(time.Second * 5).
			WithTimeout(time.Minute * 3).
			Should(Succeed())

		By("Waiting for cluster to be ready again")

		Eventually(func(g Gomega) {
			// server pods
			serverPods := listServerPods(ctx, virtualCluster)
			g.Expect(len(serverPods)).To(Equal(1))

			_, cond := pod.GetPodCondition(&serverPods[0].Status, v1.PodReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(BeEquivalentTo(metav1.ConditionTrue))

			// we can't check for number of nodes in scale down because the nodes will be there but in a non-ready state
			k8sEndpointSlices, err := virtualCluster.Client.DiscoveryV1().EndpointSlices("default").Get(ctx, "kubernetes", metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(len(k8sEndpointSlices.Endpoints)).To(Equal(1))
		}).
			MustPassRepeatedly(5).
			WithPolling(time.Second * 5).
			WithTimeout(time.Minute * 2).
			Should(Succeed())

		By("Checking that Nginx Pod is Running")

		Eventually(func(g Gomega) {
			nginxPod, err := virtualCluster.Client.CoreV1().Pods(nginxPod.Namespace).Get(ctx, nginxPod.Name, metav1.GetOptions{})
			g.Expect(err).To(BeNil())

			// TODO: there is a possible issues where the Pod is not being marked as Ready
			// if the kubelet lost the sync with the API server.
			// We check for the Running status, but this is to investigate.
			// Related issue (?): https://github.com/kubernetes/kubernetes/issues/82346

			g.Expect(nginxPod.Status.Phase).To(BeEquivalentTo(v1.PodRunning))
		}).
			WithPolling(time.Second * 2).
			WithTimeout(time.Minute).
			Should(Succeed())
	})
})
