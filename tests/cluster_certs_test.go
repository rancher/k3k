package k3k_test

import (
	"context"
	"os"
	"strings"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a cluster with custom certificates is installed with individual cert secrets", Label("e2e"), Label(certificatesTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeEach(func() {
		ctx := context.Background()

		namespace := NewNamespace()

		DeferCleanup(func() {
			DeleteNamespaces(namespace.Name)
		})

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

		cluster.Spec.CustomCAs = &v1beta1.CustomCAs{
			Enabled: true,
			Sources: v1beta1.CredentialSources{
				ServerCA: v1beta1.CredentialSource{
					SecretName: "server-ca",
				},
				ClientCA: v1beta1.CredentialSource{
					SecretName: "client-ca",
				},
				ETCDServerCA: v1beta1.CredentialSource{
					SecretName: "etcd-server-ca",
				},
				ETCDPeerCA: v1beta1.CredentialSource{
					SecretName: "etcd-peer-ca",
				},
				RequestHeaderCA: v1beta1.CredentialSource{
					SecretName: "request-header-ca",
				},
				ServiceAccountToken: v1beta1.CredentialSource{
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
		ctx := context.Background()

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
