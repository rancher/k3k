package provider

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
							LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
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
							LocalObjectReference: corev1.LocalObjectReference{Name: "my-configmap"},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configureEnv(tt.virtualPod, tt.envs)
			assert.Equal(t, tt.want, got)
		})
	}
}
