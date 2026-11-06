package e2e_test

import (
	"context"
	"io"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/policy"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// configMapsAuditPolicy logs the full requests and responses of the ConfigMaps, and nothing else.
const configMapsAuditPolicy = `apiVersion: audit.k8s.io/v1
kind: Policy
omitStages:
  - RequestReceived
rules:
  - level: RequestResponse
    resources:
      - group: ""
        resources: ["configmaps"]
  - level: None
`

var _ = When("a cluster is created with the audit log enabled", Ordered, Label(auditTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeAll(func() {
		virtualCluster = NewVirtualClusterWithOpts(func(c *v1beta1.Cluster) {
			c.Spec.Mode = v1beta1.SharedClusterMode
			c.Spec.Audit = &v1beta1.AuditConfig{
				Enabled: true,
				Policy:  configMapsAuditPolicy,
			}
		})

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})
	})

	It("streams the audit log of the custom policy from the audit-log container", func() {
		ctx := context.Background()

		configMap, marker := createAuditedConfigMap(ctx, virtualCluster)

		Eventually(func(g Gomega) {
			logs, err := auditLogs(ctx, virtualCluster)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(logs).To(ContainSubstring(configMap.Name))
			// the RequestResponse level logs the content of the ConfigMap
			g.Expect(logs).To(ContainSubstring(marker))
			// the other resources are not logged
			g.Expect(logs).NotTo(ContainSubstring(`"resource":"namespaces"`))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(Succeed())
	})
})

var _ = When("a cluster is created in a namespace with a policy enforcing the audit log", Ordered, Label(auditTestsLabel), Label(policyTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeAll(func() {
		ctx := context.Background()

		vcp := &v1beta1.VirtualClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "policy-",
			},
			Spec: v1beta1.VirtualClusterPolicySpec{
				Audit: &v1beta1.AuditConfig{Enabled: true},
			},
		}
		Expect(k8sClient.Create(ctx, vcp)).To(Succeed())

		namespace := fwk3k.CreateNamespace(k8s)

		fwk3k.SetNamespaceLabel(ctx, k8sClient, namespace.Name, policy.PolicyNameLabelKey, vcp.Name)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
			Expect(k8sClient.Delete(ctx, vcp)).To(Succeed())
		})

		// the cluster doesn't enable the audit log: it's enforced by the policy
		cluster := NewCluster(namespace.Name)

		CreateCluster(cluster)

		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})

	It("streams the audit log of the default policy from the audit-log container", func() {
		ctx := context.Background()

		configMap, marker := createAuditedConfigMap(ctx, virtualCluster)

		Eventually(func(g Gomega) {
			logs, err := auditLogs(ctx, virtualCluster)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(logs).To(ContainSubstring(configMap.Name))
			// the default policy logs only the metadata of the requests
			g.Expect(logs).NotTo(ContainSubstring(marker))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(Succeed())
	})
})

// createAuditedConfigMap creates a ConfigMap in the virtual cluster, returning it with
// the random marker stored in its data.
func createAuditedConfigMap(ctx context.Context, virtualCluster *VirtualCluster) (*corev1.ConfigMap, string) {
	GinkgoHelper()

	marker := "audit-marker-" + rand.String(10)

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "audit-",
			Namespace:    "default",
		},
		Data: map[string]string{"marker": marker},
	}

	configMap, err := virtualCluster.Client.CoreV1().ConfigMaps(configMap.Namespace).Create(ctx, configMap, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())

	return configMap, marker
}

// auditLogs reads, from the host cluster, the logs of the audit-log containers of the server pods.
func auditLogs(ctx context.Context, virtualCluster *VirtualCluster) (string, error) {
	var logs string

	for _, serverPod := range listServerPods(ctx, virtualCluster) {
		stream, err := k8s.CoreV1().Pods(serverPod.Namespace).
			GetLogs(serverPod.Name, &corev1.PodLogOptions{Container: "audit-log"}).Stream(ctx)
		if err != nil {
			return "", err
		}

		podLogs, err := io.ReadAll(stream)
		_ = stream.Close()

		if err != nil {
			return "", err
		}

		logs += string(podLogs)
	}

	return logs, nil
}
