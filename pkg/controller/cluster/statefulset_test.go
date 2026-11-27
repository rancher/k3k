package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func serverPod(name string, ready bool, deleting bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "vc1",
			Labels:    map[string]string{"cluster": "vc1", "role": "server"},
		},
	}
	if ready {
		pod.Status.Conditions = []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}
	}
	if deleting {
		now := metav1.Now()
		pod.DeletionTimestamp = &now
		pod.Finalizers = []string{etcdPodFinalizerName}
	}
	return pod
}

func Test_anyServerPodReady(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))

	tests := []struct {
		name string
		pods []*corev1.Pod
		want bool
	}{
		{
			name: "all servers down",
			pods: []*corev1.Pod{
				serverPod("k3k-vc1-server-0", false, false),
				serverPod("k3k-vc1-server-1", false, false),
				serverPod("k3k-vc1-server-2", false, true),
			},
			want: false,
		},
		{
			name: "one healthy server",
			pods: []*corev1.Pod{
				serverPod("k3k-vc1-server-0", true, false),
				serverPod("k3k-vc1-server-1", false, true),
			},
			want: true,
		},
		{
			name: "ready but deleting server does not count",
			pods: []*corev1.Pod{
				serverPod("k3k-vc1-server-0", true, true),
				serverPod("k3k-vc1-server-1", false, false),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for _, pod := range tt.pods {
				builder = builder.WithObjects(pod)
			}

			r := StatefulSetReconciler{Client: builder.Build()}

			got, err := r.anyServerPodReady(context.Background(), tt.pods[len(tt.pods)-1])
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
