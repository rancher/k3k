package upgrade_test

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	// appNamespace is the namespace, in the virtual cluster, where the test app is deployed.
	appNamespace = "default"

	// appName is both the name and the "app" label value of the test Deployment.
	appName = "nginx-upgrade-test"

	appReplicas = 2
)

// deployApp creates an nginx Deployment in the virtual cluster and waits for all
// of its replicas to be available. This is the user workload that has to survive
// the k3k upgrade untouched.
func deployApp(ctx context.Context, client *kubernetes.Clientset) {
	GinkgoHelper()

	By("Deploying the " + appName + " app")

	labels := map[string]string{"app": appName}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: appNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](appReplicas),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx",
					}},
				},
			},
		},
	}

	_, err := client.AppsV1().Deployments(appNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	Expect(err).To(Not(HaveOccurred()))

	assertAppAvailable(ctx, client)
}

// assertAppAvailable waits for the app Deployment to have all its replicas available.
func assertAppAvailable(ctx context.Context, client *kubernetes.Clientset) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		deployment, err := client.AppsV1().Deployments(appNamespace).Get(ctx, appName, metav1.GetOptions{})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(deployment.Status.AvailableReplicas).To(BeEquivalentTo(appReplicas))
	}).
		WithTimeout(time.Minute * 3).
		WithPolling(time.Second * 5).
		Should(Succeed())
}

// listAppPodUIDs returns the UIDs of the app pods, so that they can be compared
// before and after the upgrade to check that the workload was not recreated.
func listAppPodUIDs(ctx context.Context, client *kubernetes.Clientset) []types.UID {
	GinkgoHelper()

	podList, err := client.CoreV1().Pods(appNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=" + appName,
	})
	Expect(err).To(Not(HaveOccurred()))

	uids := make([]types.UID, 0, len(podList.Items))
	for _, appPod := range podList.Items {
		uids = append(uids, appPod.UID)
	}

	return uids
}
