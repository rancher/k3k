package k3k_test

import (
	"context"
	"crypto/x509"
	"errors"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("k3k is installed", Label("e2e"), func() {
	It("is in Running status", func() {
		// check that the controller is running
		Eventually(func() bool {
			opts := v1.ListOptions{LabelSelector: "app.kubernetes.io/name=k3k"}
			podList, err := k8s.CoreV1().Pods("k3k-system").List(context.Background(), opts)

			Expect(err).To(Not(HaveOccurred()))
			Expect(podList.Items).To(Not(BeEmpty()))

			var isRunning bool
			for _, pod := range podList.Items {
				if pod.Status.Phase == corev1.PodRunning {
					isRunning = true
					break
				}
			}

			return isRunning
		}).
			WithTimeout(time.Second * 10).
			WithPolling(time.Second).
			Should(BeTrue())
	})
})

var _ = When("a ephemeral cluster is installed", Label("e2e"), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		virtualCluster = NewVirtualCluster()
	})

	AfterEach(func() {
		DeleteNamespaces(virtualCluster.Cluster.Namespace)
	})

	It("can create a nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})

	It("regenerates the bootstrap secret after a restart", func() {
		ctx := context.Background()

		_, err := virtualCluster.Client.ServerVersion()
		Expect(err).To(Not(HaveOccurred()))

		labelSelector := "cluster=" + virtualCluster.Cluster.Name + ",role=server"
		serverPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))

		Expect(len(serverPods.Items)).To(Equal(1))
		serverPod := serverPods.Items[0]

		GinkgoWriter.Printf("deleting pod %s/%s\n", serverPod.Namespace, serverPod.Name)

		err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).Delete(ctx, serverPod.Name, v1.DeleteOptions{})
		Expect(err).To(Not(HaveOccurred()))

		By("Deleting server pod")

		// check that the server pods restarted
		Eventually(func() any {
			serverPods, err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
			Expect(err).To(Not(HaveOccurred()))
			Expect(len(serverPods.Items)).To(Equal(1))
			return serverPods.Items[0].DeletionTimestamp
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second * 5).
			Should(BeNil())

		By("Server pod up and running again")

		By("Using old k8s client configuration should fail")

		Eventually(func() bool {
			_, err = virtualCluster.Client.DiscoveryClient.ServerVersion()
			var unknownAuthorityErr x509.UnknownAuthorityError
			return errors.As(err, &unknownAuthorityErr)
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeTrue())

		By("Recover new config should succeed")

		Eventually(func() error {
			virtualCluster.Client, virtualCluster.RestConfig = NewVirtualK8sClientAndConfig(virtualCluster.Cluster)
			_, err = virtualCluster.Client.DiscoveryClient.ServerVersion()
			return err
		}).
			WithTimeout(time.Minute * 2).
			WithPolling(time.Second * 5).
			Should(BeNil())
	})
})

var _ = When("a dynamic cluster is installed", func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		namespace := NewNamespace()
		cluster := NewCluster(namespace.Name)
		cluster.Spec.Persistence.Type = v1alpha1.DynamicPersistenceMode
		CreateCluster(cluster)
		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})

	AfterEach(func() {
		DeleteNamespaces(virtualCluster.Cluster.Namespace)
	})

	It("can create a nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})

	It("use the same bootstrap secret after a restart", func() {
		ctx := context.Background()

		_, err := virtualCluster.Client.ServerVersion()
		Expect(err).To(Not(HaveOccurred()))

		labelSelector := "cluster=" + virtualCluster.Cluster.Name + ",role=server"
		serverPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))

		Expect(len(serverPods.Items)).To(Equal(1))
		serverPod := serverPods.Items[0]

		GinkgoWriter.Printf("deleting pod %s/%s\n", serverPod.Namespace, serverPod.Name)

		err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).Delete(ctx, serverPod.Name, v1.DeleteOptions{})
		Expect(err).To(Not(HaveOccurred()))

		By("Deleting server pod")

		// check that the server pods restarted
		Eventually(func() any {
			serverPods, err = k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
			Expect(err).To(Not(HaveOccurred()))
			Expect(len(serverPods.Items)).To(Equal(1))
			return serverPods.Items[0].DeletionTimestamp
		}).
			WithTimeout(60 * time.Second).
			WithPolling(time.Second * 5).
			Should(BeNil())

		By("Server pod up and running again")

		By("Using old k8s client configuration should succeed")

		Eventually(func() error {
			_, err = virtualCluster.Client.DiscoveryClient.ServerVersion()
			return err
		}).
			WithTimeout(2 * time.Minute).
			WithPolling(time.Second * 5).
			Should(BeNil())
	})
})

var _ = When("a cluster with custom certificates is installed with individual cert secrets", Label("e2e"), func() {
	ctx := context.Background()
	var virtualCluster *VirtualCluster
	BeforeEach(func() {
		namespace := NewNamespace()
		// create custom cert secret
		customCertDir := "testdata/customcerts/"
		certList := []string{
			"server-ca",
			"client-ca",
			"request-header-ca",
			"service",
			"etcd-peer-ca",
			"etcd-server-ca",
		}
		for _, certName := range certList {
			var cert, key []byte
			var err error
			filePathPrefix := ""
			certfile := certName
			if strings.HasPrefix(certName, "etcd") {
				filePathPrefix = "etcd/"
				certfile = strings.TrimPrefix(certName, "etcd-")
			}
			if !strings.Contains(certName, "service") {
				cert, err = os.ReadFile(customCertDir + filePathPrefix + certfile + ".crt")
				Expect(err).To(Not(HaveOccurred()))
			}
			key, err = os.ReadFile(customCertDir + filePathPrefix + certfile + ".key")
			Expect(err).To(Not(HaveOccurred()))

			certSecret := caCertSecret(certName, namespace.Name, cert, key)
			err = k8sClient.Create(ctx, certSecret)
			Expect(err).To(Not(HaveOccurred()))
		}

		cluster := NewCluster(namespace.Name)
		cluster.Spec.CustomCAs = v1alpha1.CustomCAs{
			Enabled: true,
			Sources: v1alpha1.CredentialSources{
				ServerCA: v1alpha1.CredentialSource{
					SecretName: "server-ca",
				},
				ClientCA: v1alpha1.CredentialSource{
					SecretName: "client-ca",
				},
				ETCDServerCA: v1alpha1.CredentialSource{
					SecretName: "etcd-server-ca",
				},
				ETCDPeerCA: v1alpha1.CredentialSource{
					SecretName: "etcd-peer-ca",
				},
				RequestHeaderCA: v1alpha1.CredentialSource{
					SecretName: "request-header-ca",
				},
				ServiceAccountToken: v1alpha1.CredentialSource{
					SecretName: "service",
				},
			},
		}
		CreateCluster(cluster)
		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})
	It("will load the custom certs in the server pod", func() {
		_, _ = virtualCluster.NewNginxPod("")

		labelSelector := "cluster=" + virtualCluster.Cluster.Name + ",role=server"
		serverPods, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		Expect(err).To(Not(HaveOccurred()))

		Expect(len(serverPods.Items)).To(Equal(1))
		serverPod := serverPods.Items[0]

		// check server-ca.crt
		serverCACrtPath := "/var/lib/rancher/k3s/server/tls/server-ca.crt"
		serverCACrt, err := readFileWithinPod(ctx, k8s, restcfg, serverPod.Name, serverPod.Namespace, serverCACrtPath)
		Expect(err).To(Not(HaveOccurred()))

		serverCACrtTestFile, err := os.ReadFile("testdata/customcerts/server-ca.crt")
		Expect(err).To(Not(HaveOccurred()))

		Expect(serverCACrt).To(Equal(serverCACrtTestFile))
	})
})
