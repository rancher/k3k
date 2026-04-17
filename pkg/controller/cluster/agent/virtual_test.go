package agent

import (
	"context"
	"testing"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func baseVirtualAgentPodSpec(v VirtualAgent) v1.PodSpec {
	return v1.PodSpec{
		Affinity:     nil,
		NodeSelector: v.cluster.Spec.NodeSelector,
		Volumes: []v1.Volume{
			{
				Name: "config",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: configSecretName(v.cluster.Name),
						Items: []v1.KeyToPath{
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
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varrun",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibcni",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlog",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibkubelet",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibrancherk3s",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
		},
		Containers: []v1.Container{
			{
				Name:            "k3k-agent",
				Image:           v.Image,
				ImagePullPolicy: v1.PullPolicy(v.ImagePullPolicy),
				SecurityContext: &v1.SecurityContext{
					Privileged: ptr.To(true),
				},
				Args: []string{"agent", "--config", "/opt/rancher/k3s/config.yaml"},
				Command: []string{
					"/bin/k3s",
				},
				VolumeMounts: []v1.VolumeMount{
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
		expectedPodSpec func(VirtualAgent) v1.PodSpec
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
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
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
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
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
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
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
			expectedPodSpec: func(va VirtualAgent) v1.PodSpec {
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
							AgentEnvs: []v1.EnvVar{
								{Name: "CUSTOM_VAR", Value: "custom-value"},
								{Name: "ANOTHER_VAR", Value: "another-value"},
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.Containers[0].Env = []v1.EnvVar{
					v1.EnvVar{Name: "CUSTOM_VAR", Value: "custom-value"},
					v1.EnvVar{Name: "ANOTHER_VAR", Value: "another-value"},
				}
				return spec
			},
		},
		{
			name: "agent affinity from spec is set on pod",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-affinity-spec",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							AgentAffinity: nodeAffinity("kubernetes.io/os", v1.NodeSelectorOpIn, "linux"),
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.Affinity = nodeAffinity("kubernetes.io/os", v1.NodeSelectorOpIn, "linux")
				return spec
			},
		},
		{
			name: "agent affinity from policy overrides spec",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-affinity-policy",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							AgentAffinity: nodeAffinity("spec-key", v1.NodeSelectorOpIn, "spec-value"),
						},
						Status: v1beta1.ClusterStatus{
							Policy: &v1beta1.AppliedPolicy{
								AgentAffinity: nodeAffinity("policy-key", v1.NodeSelectorOpIn, "policy-value"),
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.Affinity = nodeAffinity("policy-key", v1.NodeSelectorOpIn, "policy-value")
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
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.ImagePullSecrets = []v1.LocalObjectReference{
					{Name: "secret-1"},
					{Name: "secret-2"},
				}
				return spec
			},
		},
		{
			name: "security context from spec is set on container",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-secctx-spec",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							SecurityContext: &v1.SecurityContext{
								Privileged: ptr.To(true),
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.Containers[0].SecurityContext = &v1.SecurityContext{
					Privileged: ptr.To(true),
				}
				return spec
			},
		},
		{
			name: "security context from policy overrides spec",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-secctx-policy",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							SecurityContext: &v1.SecurityContext{
								Privileged: ptr.To(true),
							},
						},
						Status: v1beta1.ClusterStatus{
							Policy: &v1beta1.AppliedPolicy{
								SecurityContext: &v1.SecurityContext{
									Privileged:             ptr.To(false),
									ReadOnlyRootFilesystem: ptr.To(true),
								},
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.Containers[0].SecurityContext = &v1.SecurityContext{
					Privileged:             ptr.To(false),
					ReadOnlyRootFilesystem: ptr.To(true),
				}
				return spec
			},
		},
		{
			name: "runtime class name from spec is set on pod",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-runtime-spec",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							RuntimeClassName: ptr.To("kata"),
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.RuntimeClassName = ptr.To("kata")
				return spec
			},
		},
		{
			name: "runtime class name from policy overrides spec",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-runtime-policy",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							RuntimeClassName: ptr.To("kata"),
						},
						Status: v1beta1.ClusterStatus{
							Policy: &v1beta1.AppliedPolicy{
								RuntimeClassName: ptr.To("gvisor"),
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.RuntimeClassName = ptr.To("gvisor")
				return spec
			},
		},
		{
			name: "host users from spec is set on pod",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-hostusers-spec",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							HostUsers: ptr.To(false),
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.HostUsers = ptr.To(false)
				return spec
			},
		},
		{
			name: "host users from policy overrides spec",
			virtualAgent: VirtualAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vc-hostusers-policy",
							Namespace: "virtual-test",
						},
						Spec: v1beta1.ClusterSpec{
							HostUsers: ptr.To(true),
						},
						Status: v1beta1.ClusterStatus{
							Policy: &v1beta1.AppliedPolicy{
								HostUsers: ptr.To(false),
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.HostUsers = ptr.To(false)
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
							WorkerLimit: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("500m"),
								v1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					},
				},
				Image: "rancher/k3k:latest",
			},
			expectedPodSpec: func(sa VirtualAgent) v1.PodSpec {
				spec := baseVirtualAgentPodSpec(sa)
				spec.Containers[0].Resources = v1.ResourceRequirements{
					Limits: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("500m"),
						v1.ResourceMemory: resource.MustParse("256Mi"),
					},
				}
				return spec
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			podSpec := tt.virtualAgent.podSpec(context.Background(), tt.virtualAgent.Image, "k3k-agent")
			assert.Equal(t, tt.expectedPodSpec(tt.virtualAgent), podSpec)
		})
	}
}
