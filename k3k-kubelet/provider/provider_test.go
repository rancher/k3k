package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

func Test_mergeEnvVars(t *testing.T) {
	type args struct {
		orig []corev1.EnvVar
		new  []corev1.EnvVar
	}

	tests := []struct {
		name string
		args args
		want []corev1.EnvVar
	}{
		{
			name: "orig and new are empty",
			args: args{
				orig: []corev1.EnvVar{},
				new:  []corev1.EnvVar{},
			},
			want: []corev1.EnvVar{},
		},
		{
			name: "only orig is empty",
			args: args{
				orig: []corev1.EnvVar{},
				new:  []corev1.EnvVar{{Name: "FOO", Value: "new_val"}},
			},
			want: []corev1.EnvVar{{Name: "FOO", Value: "new_val"}},
		},
		{
			name: "orig has a matching element",
			args: args{
				orig: []corev1.EnvVar{{Name: "FOO", Value: "old_val"}},
				new:  []corev1.EnvVar{{Name: "FOO", Value: "new_val"}},
			},
			want: []corev1.EnvVar{{Name: "FOO", Value: "new_val"}},
		},
		{
			name: "orig have multiple elements",
			args: args{
				orig: []corev1.EnvVar{{Name: "FOO_0", Value: "old_val_0"}, {Name: "FOO_1", Value: "old_val_1"}},
				new:  []corev1.EnvVar{{Name: "FOO_1", Value: "new_val_1"}},
			},
			want: []corev1.EnvVar{{Name: "FOO_0", Value: "old_val_0"}, {Name: "FOO_1", Value: "new_val_1"}},
		},
		{
			name: "orig and new have multiple elements and some not matching",
			args: args{
				orig: []corev1.EnvVar{{Name: "FOO_0", Value: "old_val_0"}, {Name: "FOO_1", Value: "old_val_1"}},
				new:  []corev1.EnvVar{{Name: "FOO_1", Value: "new_val_1"}, {Name: "FOO_2", Value: "val_1"}},
			},
			want: []corev1.EnvVar{{Name: "FOO_0", Value: "old_val_0"}, {Name: "FOO_1", Value: "new_val_1"}, {Name: "FOO_2", Value: "val_1"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeEnvVars(tt.args.orig, tt.args.new); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeEnvVars() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_configureEnv(t *testing.T) {
	virtualPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "my-namespace",
		},
	}

	tests := []struct {
		name       string
		virtualPod *corev1.Pod
		envs       []corev1.EnvVar
		want       []corev1.EnvVar
	}{
		{
			name:       "empty envs",
			virtualPod: virtualPod,
			envs:       []corev1.EnvVar{},
			want:       []corev1.EnvVar{},
		},
		{
			name:       "simple env var",
			virtualPod: virtualPod,
			envs: []corev1.EnvVar{
				{Name: "MY_VAR", Value: "my-value"},
			},
			want: []corev1.EnvVar{
				{Name: "MY_VAR", Value: "my-value"},
			},
		},
		{
			name:       "metadata.name field ref",
			virtualPod: virtualPod,
			envs: []corev1.EnvVar{
				{
					Name: "POD_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.name",
						},
					},
				},
			},
			want: []corev1.EnvVar{
				{Name: "POD_NAME", Value: "my-pod"},
			},
		},
		{
			name:       "metadata.namespace field ref",
			virtualPod: virtualPod,
			envs: []corev1.EnvVar{
				{
					Name: "POD_NAMESPACE",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.namespace",
						},
					},
				},
			},
			want: []corev1.EnvVar{
				{Name: "POD_NAMESPACE", Value: "my-namespace"},
			},
		},
		{
			name:       "other field ref",
			virtualPod: virtualPod,
			envs: []corev1.EnvVar{
				{
					Name: "NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
			},
			want: []corev1.EnvVar{
				{
					Name: "NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
			},
		},
		{
			name:       "secret key ref",
			virtualPod: virtualPod,
			envs: []corev1.EnvVar{
				{
					Name: "SECRET_VAR",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
							Key:                  "my-key",
						},
					},
				},
			},
			want: []corev1.EnvVar{
				{
					Name: "SECRET_VAR",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret-my-namespace-c-test-6d792d7365637265742b6d792d6-887db"},
							Key:                  "my-key",
						},
					},
				},
			},
		},
		{
			name:       "configmap key ref",
			virtualPod: virtualPod,
			envs: []corev1.EnvVar{
				{
					Name: "CONFIG_VAR",
					ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "my-configmap"},
							Key:                  "my-key",
						},
					},
				},
			},
			want: []corev1.EnvVar{
				{
					Name: "CONFIG_VAR",
					ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "my-configmap-my-namespace-c-test-6d792d636f6e6669676d6170-301f6"},
							Key:                  "my-key",
						},
					},
				},
			},
		},
		{
			name:       "resource field ref",
			virtualPod: virtualPod,
			envs: []corev1.EnvVar{
				{
					Name: "CPU_LIMIT",
					ValueFrom: &corev1.EnvVarSource{
						ResourceFieldRef: &corev1.ResourceFieldSelector{
							ContainerName: "my-container",
							Resource:      "limits.cpu",
						},
					},
				},
			},
			want: []corev1.EnvVar{
				{
					Name: "CPU_LIMIT",
					ValueFrom: &corev1.EnvVarSource{
						ResourceFieldRef: &corev1.ResourceFieldSelector{
							ContainerName: "my-container",
							Resource:      "limits.cpu",
						},
					},
				},
			},
		},
		{
			name:       "mixed env vars",
			virtualPod: virtualPod,
			envs: []corev1.EnvVar{
				{Name: "MY_VAR", Value: "my-value"},
				{
					Name: "POD_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.name",
						},
					},
				},
				{
					Name: "POD_NAMESPACE",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.namespace",
						},
					},
				},
				{
					Name: "NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
			},
			want: []corev1.EnvVar{
				{Name: "MY_VAR", Value: "my-value"},
				{Name: "POD_NAME", Value: "my-pod"},
				{Name: "POD_NAMESPACE", Value: "my-namespace"},
				{
					Name: "NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
			},
		},
	}

	p := Provider{
		Translator: translate.ToHostTranslator{
			ClusterName:      "c-test",
			ClusterNamespace: "ns-test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.configureEnv(tt.virtualPod, tt.envs)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestGetPods_ScopedToVirtualNode pins the behavior that GetPods returns only the Pods this
// instance owns -- determined by the *virtual* Pod's node (spec.nodeName == agentHostname), the
// same signal the virtual-kubelet framework's deleteDanglingPods uses -- plus genuinely dangling
// Pods (whose virtual counterpart no longer exists). Pods owned by another node are excluded so
// this instance never treats them as dangling and deletes them. The host Pod's own physical
// spec.nodeName is unrelated to ownership and must be ignored.
func TestGetPods_ScopedToVirtualNode(t *testing.T) {
	const clusterName = "c-test"

	// host Pods carry the tracking metadata TranslateFrom reads to recover the virtual identity.
	// Their physical NodeName is set to deliberately-mismatched values to prove it is ignored.
	newHostPod := func(hostName, virtName, hostNodeName string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostName,
				Namespace: "ns-test",
				Labels: map[string]string{
					translate.ClusterNameLabel: clusterName,
				},
				Annotations: map[string]string{
					translate.ResourceNameAnnotation:      virtName,
					translate.ResourceNamespaceAnnotation: "default",
				},
			},
			Spec: corev1.PodSpec{NodeName: hostNodeName},
		}
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	hostClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			newHostPod("host-a1", "a1", "node-b"), // owned by node-a (virtual), physically on node-b
			newHostPod("host-b1", "b1", "node-a"), // owned by node-b (virtual), physically on node-a
			newHostPod("host-c1", "c1", "node-a"), // dangling: no virtual Pod exists
		).
		Build()

	// virtual Pods: a1 on node-a (ours), b1 on node-b (other); c1 intentionally absent (dangling).
	virtClient := fakeclientset.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "a1", Namespace: "default"},
			Spec:       corev1.PodSpec{NodeName: "node-a"},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "default"},
			Spec:       corev1.PodSpec{NodeName: "node-b"},
		},
	)

	p := Provider{
		Host:    ClusterContext{Client: hostClient},
		Virtual: ClusterContext{CoreClient: virtClient.CoreV1()},
		Translator: translate.ToHostTranslator{
			ClusterName:      clusterName,
			ClusterNamespace: "ns-test",
		},
		ClusterName:   clusterName,
		agentHostname: "node-a",
		logger:        logr.Discard(),
	}

	pods, err := p.GetPods(context.Background())
	require.NoError(t, err)

	names := map[string]bool{}
	for _, pod := range pods {
		names[pod.Name] = true
	}

	// a1 (own node) and c1 (dangling) are returned; b1 (other node) is excluded.
	assert.Equal(t, map[string]bool{"a1": true, "c1": true}, names)
}
