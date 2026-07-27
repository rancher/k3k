package upgrade_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/pkg/api/v1/pod"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	sigsclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type VirtualCluster struct {
	Cluster    *v1beta1.Cluster
	RestConfig *rest.Config
	Client     *kubernetes.Clientset
}

func NewCluster(namespace string, opts ...func(*v1beta1.Cluster)) *v1beta1.Cluster {
	c := &v1beta1.Cluster{
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
		optFn(c)
	}

	return c
}

func CreateCluster(cluster *v1beta1.Cluster) {
	GinkgoHelper()

	By(fmt.Sprintf("Creating new virtual cluster in namespace %s", cluster.Namespace))

	ctx := context.Background()
	err := k8sClient.Create(ctx, cluster)
	Expect(err).To(Not(HaveOccurred()))

	expectedServers := int(*cluster.Spec.Servers)
	expectedAgents := int(*cluster.Spec.Agents)

	By(fmt.Sprintf("Waiting for cluster %s to be ready in namespace %s. Expected servers: %d. Expected agents: %d", cluster.Name, cluster.Namespace, expectedServers, expectedAgents))

	// check that the server Pod and the Kubelet are in Ready state
	Eventually(func() bool {
		podList, err := k8s.CoreV1().Pods(cluster.Namespace).List(ctx, metav1.ListOptions{})
		Expect(err).To(Not(HaveOccurred()))

		var serversReady, agentsReady int

		for _, k3sPod := range podList.Items {
			_, cond := pod.GetPodCondition(&k3sPod.Status, corev1.PodReady)

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

func NewVirtualK8sClientAndConfig(cluster *v1beta1.Cluster) (*kubernetes.Clientset, *rest.Config) {
	GinkgoHelper()

	var (
		err    error
		config *clientcmdapi.Config
	)

	ctx := context.Background()

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

	return virtualK8sClient, restcfg
}

func listServerPods(ctx context.Context, virtualCluster *VirtualCluster) []corev1.Pod {
	GinkgoHelper()

	podList, err := k8s.CoreV1().Pods(virtualCluster.Cluster.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("cluster=%s,role=server", virtualCluster.Cluster.Name),
	})
	Expect(err).To(Not(HaveOccurred()))

	return podList.Items
}

func patchPVC(ctx context.Context, clientset *kubernetes.Clientset) {
	deployments, err := clientset.AppsV1().Deployments(k3kNamespace).List(ctx, metav1.ListOptions{})
	Expect(err).To(Not(HaveOccurred()))
	Expect(deployments.Items).To(HaveLen(1))

	k3kDeployment := &deployments.Items[0]

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coverage-data-pvc",
			Namespace: k3kNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("100M"),
				},
			},
		},
	}

	_, err = clientset.CoreV1().PersistentVolumeClaims(k3kNamespace).Create(ctx, pvc, metav1.CreateOptions{})
	Expect(sigsclient.IgnoreAlreadyExists(err)).To(Not(HaveOccurred()))

	k3kSpec := k3kDeployment.Spec.Template.Spec

	// check if the Deployment has already the volume for the coverage
	for _, volumes := range k3kSpec.Volumes {
		if volumes.Name == "tmp-covdata" {
			return
		}
	}

	k3kSpec.Volumes = append(k3kSpec.Volumes, corev1.Volume{
		Name: "tmp-covdata",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "coverage-data-pvc",
			},
		},
	})

	k3kSpec.Containers[0].VolumeMounts = append(k3kSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      "tmp-covdata",
		MountPath: "/tmp/covdata",
	})

	k3kSpec.Containers[0].Env = append(k3kSpec.Containers[0].Env, corev1.EnvVar{
		Name:  "GOCOVERDIR",
		Value: "/tmp/covdata",
	})

	k3kDeployment.Spec.Template.Spec = k3kSpec

	_, err = clientset.AppsV1().Deployments(k3kNamespace).Update(ctx, k3kDeployment, metav1.UpdateOptions{})
	Expect(err).To(Not(HaveOccurred()))

	Eventually(func(g Gomega) {
		GinkgoWriter.Println("Checking K3k deployment status")

		dep, err := clientset.AppsV1().Deployments(k3kNamespace).Get(ctx, k3kDeployment.Name, metav1.GetOptions{})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(dep.Generation).To(Equal(dep.Status.ObservedGeneration))

		var availableCond appsv1.DeploymentCondition

		for _, cond := range dep.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable {
				availableCond = cond
				break
			}
		}

		g.Expect(availableCond.Type).To(Equal(appsv1.DeploymentAvailable))
		g.Expect(availableCond.Status).To(Equal(corev1.ConditionTrue))
	}).
		WithPolling(time.Second).
		WithTimeout(time.Second * 30).
		Should(Succeed())
}
