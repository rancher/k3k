package k3k_test

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("creating a shared mode cluster with postgres datastore via server args", Ordered, Label(datastoreTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeAll(func() {
		namespace := fwk3k.CreateNamespace(k8s)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
		})

		postgresEndpoint := fmt.Sprintf("postgres-k3k.%s.svc.cluster.local:5432", namespace.Name)

		dsn := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(postgresUser, postgresPassword),
			Host:     postgresEndpoint,
			Path:     postgresDatabase,
			RawQuery: "sslmode=disable",
		}

		cluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
			c.Spec.ServerArgs = []string{
				fmt.Sprintf("--datastore-endpoint=%s", dsn.String()),
				"--cluster-init=false",
			}
		})

		deployPostgresInCluster(cluster)

		CreateCluster(cluster)
		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})
	It("creates and writes to a database for the cluster", func() {
		Eventually(func(g Gomega) {
			ctx := GinkgoT().Context()

			count, err := queryPostgres(ctx, virtualCluster.Cluster.Namespace, "SELECT count(*) FROM kine WHERE name LIKE '/registry/namespaces/%'")
			g.Expect(err).To(Not(HaveOccurred()))

			g.Expect(strconv.Atoi(count)).To(BeNumerically(">", 0))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})
	It("creates server pods with no etcd finalizers", func() {
		Consistently(func(g Gomega) {
			serverPods := listServerPods(GinkgoT().Context(), virtualCluster)
			for _, s := range serverPods {
				g.Expect(s.Finalizers).To(BeEmpty())
			}
		}).
			WithTimeout(time.Second * 30).
			WithPolling(time.Second * 2).
			Should(Succeed())
	})
	It("can create a nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})
})

var _ = When("creating a shared mode cluster with postgres datastore via drop-in config file", Ordered, Label(datastoreTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeAll(func() {
		ctx := GinkgoT().Context()

		namespace := fwk3k.CreateNamespace(k8s)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
		})

		configSecretName := "datastore-config-secret"
		configSecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configSecretName,
				Namespace: namespace.Name,
			},
			Data: map[string][]byte{
				"config.yaml": []byte(fmt.Sprintf(
					"datastore-endpoint: postgres://%s:%s@postgres-k3k.%s.svc.cluster.local:%d/%s?sslmode=disable\ncluster-init: false\n",
					postgresUser, postgresPassword, namespace.Name, postgresPort, postgresDatabase,
				)),
			},
		}

		err := k8sClient.Create(ctx, &configSecret)
		Expect(err).NotTo(HaveOccurred())

		cluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
			c.Spec.SecretMounts = []v1beta1.SecretMount{
				{
					Name: "external-datastore-init-config",
					SecretVolumeSource: corev1.SecretVolumeSource{
						SecretName: configSecretName,
					},
					MountPath: "/opt/rancher/k3s/init/config.yaml.d/",
				},
			}
		})

		deployPostgresInCluster(cluster)

		CreateCluster(cluster)
		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})
	It("creates and writes to a database for the cluster", func() {
		Eventually(func(g Gomega) {
			ctx := GinkgoT().Context()

			count, err := queryPostgres(ctx, virtualCluster.Cluster.Namespace, "SELECT count(*) FROM kine WHERE name LIKE '/registry/namespaces/%'")
			g.Expect(err).To(Not(HaveOccurred()))

			g.Expect(strconv.Atoi(count)).To(BeNumerically(">", 0))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})
	It("creates server pods with no etcd finalizers", func() {
		Consistently(func(g Gomega) {
			serverPods := listServerPods(GinkgoT().Context(), virtualCluster)
			for _, s := range serverPods {
				g.Expect(s.Finalizers).To(BeEmpty())
			}
		}).
			WithTimeout(time.Second * 30).
			WithPolling(time.Second * 2).
			Should(Succeed())
	})
	It("can create a nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})
})

var _ = When("creating a virtual mode cluster with postgres datastore via server args", Ordered, Label(datastoreTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeAll(func() {
		namespace := fwk3k.CreateNamespace(k8s)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
		})

		postgresEndpoint := fmt.Sprintf("postgres-k3k.%s.svc.cluster.local:5432", namespace.Name)

		dsn := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(postgresUser, postgresPassword),
			Host:     postgresEndpoint,
			Path:     postgresDatabase,
			RawQuery: "sslmode=disable",
		}

		cluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
			c.Spec.ServerArgs = []string{
				fmt.Sprintf("--datastore-endpoint=%s", dsn.String()),
				"--cluster-init=false",
			}
		})

		deployPostgresInCluster(cluster)

		CreateCluster(cluster)
		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})
	It("creates and writes to a database for the cluster", func() {
		Eventually(func(g Gomega) {
			ctx := GinkgoT().Context()

			count, err := queryPostgres(ctx, virtualCluster.Cluster.Namespace, "SELECT count(*) FROM kine WHERE name LIKE '/registry/namespaces/%'")
			g.Expect(err).To(Not(HaveOccurred()))

			g.Expect(strconv.Atoi(count)).To(BeNumerically(">", 0))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})
	It("creates server pods with no etcd finalizers", func() {
		Consistently(func(g Gomega) {
			serverPods := listServerPods(GinkgoT().Context(), virtualCluster)
			for _, s := range serverPods {
				g.Expect(s.Finalizers).To(BeEmpty())
			}
		}).
			WithTimeout(time.Second * 30).
			WithPolling(time.Second * 2).
			Should(Succeed())
	})
	It("can create a nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})
})

var _ = When("creating a virtual mode cluster with postgres datastore via drop-in config file", Ordered, Label(datastoreTestsLabel), Label(slowTestsLabel), func() {
	var virtualCluster *VirtualCluster

	BeforeAll(func() {
		ctx := GinkgoT().Context()

		namespace := fwk3k.CreateNamespace(k8s)

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, namespace.Name)
		})

		configSecretName := "datastore-config-secret"
		configSecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configSecretName,
				Namespace: namespace.Name,
			},
			Data: map[string][]byte{
				"config.yaml": []byte(fmt.Sprintf(
					"datastore-endpoint: postgres://%s:%s@postgres-k3k.%s.svc.cluster.local:%d/%s?sslmode=disable\ncluster-init: false\n",
					postgresUser, postgresPassword, namespace.Name, postgresPort, postgresDatabase,
				)),
			},
		}

		err := k8sClient.Create(ctx, &configSecret)
		Expect(err).NotTo(HaveOccurred())

		cluster := NewCluster(namespace.Name, func(c *v1beta1.Cluster) {
			c.Spec.Mode = v1beta1.VirtualClusterMode
			c.Spec.SecretMounts = []v1beta1.SecretMount{
				{
					Name: "external-datastore-init-config",
					SecretVolumeSource: corev1.SecretVolumeSource{
						SecretName: configSecretName,
					},
					MountPath: "/opt/rancher/k3s/init/config.yaml.d/",
				},
			}
		})

		deployPostgresInCluster(cluster)

		CreateCluster(cluster)
		client, restConfig := NewVirtualK8sClientAndConfig(cluster)

		virtualCluster = &VirtualCluster{
			Cluster:    cluster,
			RestConfig: restConfig,
			Client:     client,
		}
	})
	It("creates and writes to a database for the cluster", func() {
		Eventually(func(g Gomega) {
			ctx := GinkgoT().Context()

			count, err := queryPostgres(ctx, virtualCluster.Cluster.Namespace, "SELECT count(*) FROM kine WHERE name LIKE '/registry/namespaces/%'")
			g.Expect(err).To(Not(HaveOccurred()))

			g.Expect(strconv.Atoi(count)).To(BeNumerically(">", 0))
		}).
			WithTimeout(time.Minute).
			WithPolling(time.Second).
			Should(Succeed())
	})
	It("creates server pods with no etcd finalizers", func() {
		Consistently(func(g Gomega) {
			serverPods := listServerPods(GinkgoT().Context(), virtualCluster)
			for _, s := range serverPods {
				g.Expect(s.Finalizers).To(BeEmpty())
			}
		}).
			WithTimeout(time.Second * 30).
			WithPolling(time.Second * 2).
			Should(Succeed())
	})
	It("can create a nginx pod", func() {
		_, _ = virtualCluster.NewNginxPod("")
	})
})
