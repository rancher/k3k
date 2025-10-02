package k3k_test

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a shared mode cluster update its envs", Label("e2e"), Label("update"), func() {
	var virtualCluster *VirtualCluster
	ctx := context.Background()
	BeforeEach(func() {
		namespace := NewNamespace()

		cluster := NewCluster(namespace.Name)

		// Add initial environment variables for server
		cluster.Spec.ServerEnvs = []corev1.EnvVar{
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
		cluster.Spec.AgentEnvs = []corev1.EnvVar{
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
			var cluster v1alpha1.Cluster

			err := k8sClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(virtualCluster.Cluster), &cluster)
			g.Expect(err).NotTo(HaveOccurred())

			// update both agent and server envs
			cluster.Spec.ServerEnvs = []corev1.EnvVar{
				{
					Name:  "TEST_SERVER_ENV_1",
					Value: "upgraded",
				},
				{
					Name:  "TEST_SERVER_ENV_3",
					Value: "new",
				},
			}
			cluster.Spec.AgentEnvs = []corev1.EnvVar{
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

var _ = When("a shared mode cluster update its server args", Label("e2e"), Label("update"), func() {
	var virtualCluster *VirtualCluster
	ctx := context.Background()
	BeforeEach(func() {
		namespace := NewNamespace()

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
			var cluster v1alpha1.Cluster

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
