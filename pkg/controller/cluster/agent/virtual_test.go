package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func baseVirtualAgentPodSpec(v VirtualAgent) corev1.PodSpec {
	return corev1.PodSpec{
		Affinity:     nil,
		NodeSelector: v.cluster.Spec.NodeSelector,
		Volumes: []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: configSecretName(v.cluster.Name),
						Items: []corev1.KeyToPath{
							{
								Key:  "config.yaml",
								Path: "config.yaml",
							},
						},
					},
				},
			},
			{
				Name: "run",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varrun",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibcni",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlog",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibkubelet",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibrancherk3s",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
		Containers: []corev1.Container{
			{
				Name:            "k3k-agent",
				Image:           v.Image,
				ImagePullPolicy: corev1.PullPolicy(v.ImagePullPolicy),
				SecurityContext: &corev1.SecurityContext{
					Privileged: ptr.To(true),
				},
				Args: []string{"agent", "--config", "/opt/rancher/k3s/config.yaml"},
				Command: []string{
					"/bin/k3s",
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "config",
						MountPath: "/opt/rancher/k3s/",
						ReadOnly:  false,
					},
					{
						Name:      "run",
						MountPath: "/run",
						ReadOnly:  false,
					},
					{
						Name:      "varrun",
						MountPath: "/var/run",
						ReadOnly:  false,
					},
					{
						Name:      "varlibcni",
						MountPath: "/var/lib/cni",
						ReadOnly:  false,
					},
					{
						Name:      "varlibkubelet",
						MountPath: "/var/lib/kubelet",
						ReadOnly:  false,
					},
					{
						Name:      "varlibrancherk3s",
						MountPath: "/var/lib/rancher/k3s",
						ReadOnly:  false,
					},
					{
						Name:      "varlog",
						MountPath: "/var/log",
						ReadOnly:  false,
					},
				},
			},
		},
	}
}

func Test_virtualAgentData(t *testing.T) {
	type args struct {
		serviceIP string
		token     string
	}

	tests := []struct {
		name         string
		args         args
		expectedData map[string]string
	}{
		{
			name: "simple config",
			args: args{
				serviceIP: "10.0.0.21",
				token:     "dnjklsdjnksd892389238",
			},
			expectedData: map[string]string{
				"server":       "https://10.0.0.21",
				"token":        "dnjklsdjnksd892389238",
				"with-node-id": "true",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := virtualAgentData(tt.args.serviceIP, tt.args.token)

			data := make(map[string]string)
			err := yaml.Unmarshal([]byte(config), data)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedData, data)
		})
	}
}

func Test_virtualAgentPodSpec(t *testing.T) {
	tests := []struct {
		name            string
		virtualAgent    VirtualAgent
		expectedPodSpec func(VirtualAgent) corev1.PodSpec
	}{
		{
			name: "default virtual mode cluster",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Cluster",
							APIVersion: "k3k.io/v1beta",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-default",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) corev1.PodSpec {
				return baseVirtualAgentPodSpec(sa)
			},
		},
		{
			name: "image registry is prepended and image pull policy is applied",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-image",
							Namespace: "virtual-test",
						},
					},
				},
				Image:           "rancher/k3k:v1.2.3",
				ImageRegistry:   "registry.example.com",
				ImagePullPolicy: "Always",
			},
			expectedPodSpec: func(sa VirtualAgent) corev1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.Containers[0].Image = "registry.example.com/rancher/k3k:v1.2.3"

				return spec
			},
		},
		{
			name: "node selector from spec is set on pod",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-nodeselector",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							NodeSelector: map[string]string{
								"disktype":             "ssd",
								"topology.k8s.io/zone": "us-east-1a",
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) corev1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.NodeSelector = map[string]string{
					"disktype":             "ssd",
					"topology.k8s.io/zone": "us-east-1a",
				}

				return spec
			},
		},
		{
			name: "agent args from spec are appended to default args",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-agentenvs",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							AgentArgs: []string{
								"fake-arg-1=true",
								"fake-arg-2=true",
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(va VirtualAgent) corev1.PodSpec {
				spec := baseVirtualAgentPodSpec(va)
				spec.Containers[0].Args = append(spec.Containers[0].Args, "fake-arg-1=true", "fake-arg-2=true")

				return spec
			},
		},
		{
			name: "agent envs from spec are appended to default env",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-agentenvs",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							AgentEnvs: []corev1.EnvVar{
								{Name: "CUSTOM_VAR", Value: "custom-value"},
								{Name: "ANOTHER_VAR", Value: "another-value"},
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) corev1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.Containers[0].Env = []corev1.EnvVar{
					{Name: "CUSTOM_VAR", Value: "custom-value"},
					{Name: "ANOTHER_VAR", Value: "another-value"},
				}

				return spec
			},
		},
		{
			name: "image pull secrets are set on pod",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-pullsecrets",
							Namespace: "virtual-test",
						},
					},
				},
				Image: "rancher/k3k:latest",

				imagePullSecrets: []string{"secret-1", "secret-2"},
			},
			expectedPodSpec: func(sa VirtualAgent) corev1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.ImagePullSecrets = []corev1.LocalObjectReference{
					{Name: "secret-1"},
					{Name: "secret-2"},
				}

				return spec
			},
		},
		{
			name: "worker limit sets container resource limits",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-workerlimit",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							WorkerLimit: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) corev1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				}

				return spec
			},
		},
		{
			name: "worker resources sets pods resource limits/requests",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-workerResources",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							WorkerResources: &corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(va VirtualAgent) corev1.PodSpec {
				spec := baseVirtualAgentPodSpec(va)
				spec.Resources = &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				}

				return spec
			},
		},
		{
			name: "worker resources takes precedence over WorkerLimit",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-workerResources",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							WorkerLimit: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
							WorkerResources: &corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(va VirtualAgent) corev1.PodSpec {
				spec := baseVirtualAgentPodSpec(va)
				spec.Resources = &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				}
				spec.Containers[0].Resources = corev1.ResourceRequirements{}

				return spec
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			podSpec := tt.virtualAgent.podSpec(tt.virtualAgent.Image, "k3k-agent")
			assert.Equal(t, tt.expectedPodSpec(tt.virtualAgent), podSpec)
		})
	}
}
