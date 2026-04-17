package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func baseSharedAgentPodSpec(sharedAgent SharedAgent) v1.PodSpec {
	return v1.PodSpec{
		Affinity:           nil,
		HostNetwork:        false,
		DNSPolicy:          v1.DNSClusterFirst,
		ServiceAccountName: sharedAgent.Name(),
		NodeSelector:       nil,
		Volumes: []v1.Volume{
			{
				Name: "config",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: configSecretName(sharedAgent.cluster.Name),
						Items: []v1.KeyToPath{
							{
								Key:  "config.yaml",
								Path: "config.yaml",
							},
						},
					},
				},
			},
		},
		Containers: []v1.Container{
			{
				Name:            sharedAgent.Name(),
				Image:           sharedAgent.image,
				ImagePullPolicy: v1.PullPolicy(sharedAgent.imagePullPolicy),
				Env: []v1.EnvVar{
					{
						Name: "AGENT_HOSTNAME",
						ValueFrom: &v1.EnvVarSource{
							FieldRef: &v1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "spec.nodeName",
							},
						},
					},
					{
						Name: "POD_IP",
						ValueFrom: &v1.EnvVarSource{
							FieldRef: &v1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "status.podIP",
							},
						},
					},
				},
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      "config",
						MountPath: "/opt/rancher/k3k/",
						ReadOnly:  false,
					},
				},
				Ports: []v1.ContainerPort{
					{
						Name:          "kubelet-port",
						Protocol:      v1.ProtocolTCP,
						ContainerPort: int32(sharedAgent.kubeletPort),
					},
				},
			},
		},
	}

}

func nodeAffinity(key string, op v1.NodeSelectorOperator, value string) *corev1.Affinity {
	return &v1.Affinity{
		NodeAffinity: &v1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{
						MatchExpressions: []v1.NodeSelectorRequirement{
							{Key: key, Operator: op, Values: []string{value}},
						},
					},
				},
			},
		},
	}
}

func Test_sharedAgentPodSpec(t *testing.T) {
	tests := []struct {
		name            string
		sharedAgent     SharedAgent
		expectedPodSpec func(SharedAgent) v1.PodSpec
	}{
		{
			name: "default shared cluster",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Cluster",
							APIVersion: "k3k.io/v1beta",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-default",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				return baseSharedAgentPodSpec(sa)
			},
		},
		{
			name: "mirror host nodes enables host networking",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-mirror",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							MirrorHostNodes: true,
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.HostNetwork = true
				spec.DNSPolicy = v1.DNSClusterFirstWithHostNet

				return spec
			},
		},
		{
			name: "image registry is prepended and image pull policy is applied",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-image",
							Namespace: "shared-test",
						},
					},
				},
				image:           "rancher/k3k-kubelet:v1.2.3",
				imageRegistry:   "registry.example.com",
				imagePullPolicy: "Always",
				kubeletPort:     10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.Containers[0].Image = "registry.example.com/rancher/k3k-kubelet:v1.2.3"

				return spec
			},
		},
		{
			name: "node selector from spec is set on pod",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-nodeselector",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							NodeSelector: map[string]string{
								"disktype":             "ssd",
								"topology.k8s.io/zone": "us-east-1a",
							},
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.NodeSelector = map[string]string{
					"disktype":             "ssd",
					"topology.k8s.io/zone": "us-east-1a",
				}

				return spec
			},
		},
		{
			name: "agent envs from spec are appended to default env",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-agentenvs",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							AgentEnvs: []v1.EnvVar{
								{Name: "CUSTOM_VAR", Value: "custom-value"},
								{Name: "ANOTHER_VAR", Value: "another-value"},
							},
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.Containers[0].Env = append(spec.Containers[0].Env,
					v1.EnvVar{Name: "CUSTOM_VAR", Value: "custom-value"},
					v1.EnvVar{Name: "ANOTHER_VAR", Value: "another-value"},
				)

				return spec
			},
		},
		{
			name: "agent affinity from spec is set on pod",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-affinity-spec",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							AgentAffinity: nodeAffinity("kubernetes.io/os", v1.NodeSelectorOpIn, "linux"),
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.Affinity = nodeAffinity("kubernetes.io/os", v1.NodeSelectorOpIn, "linux")

				return spec
			},
		},
		{
			name: "agent affinity from policy overrides spec",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-affinity-policy",
							Namespace: "shared-test",
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
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.Affinity = nodeAffinity("policy-key", v1.NodeSelectorOpIn, "policy-value")

				return spec
			},
		},
		{
			name: "image pull secrets are set on pod",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-pullsecrets",
							Namespace: "shared-test",
						},
					},
				},
				image:            "rancher/k3k-kubelet:latest",
				kubeletPort:      10250,
				imagePullSecrets: []string{"secret-1", "secret-2"},
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.ImagePullSecrets = []v1.LocalObjectReference{
					{Name: "secret-1"},
					{Name: "secret-2"},
				}

				return spec
			},
		},
		{
			name: "security context from spec is set on container",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-secctx-spec",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							SecurityContext: &v1.SecurityContext{
								Privileged: ptr.To(true),
							},
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.Containers[0].SecurityContext = &v1.SecurityContext{
					Privileged: ptr.To(true),
				}

				return spec
			},
		},
		{
			name: "security context from policy overrides spec",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-secctx-policy",
							Namespace: "shared-test",
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
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.Containers[0].SecurityContext = &v1.SecurityContext{
					Privileged:             ptr.To(false),
					ReadOnlyRootFilesystem: ptr.To(true),
				}

				return spec
			},
		},
		{
			name: "runtime class name from spec is set on pod",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-runtime-spec",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							RuntimeClassName: ptr.To("kata"),
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.RuntimeClassName = ptr.To("kata")

				return spec
			},
		},
		{
			name: "runtime class name from policy overrides spec",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-runtime-policy",
							Namespace: "shared-test",
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
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.RuntimeClassName = ptr.To("gvisor")

				return spec
			},
		},
		{
			name: "host users from spec is set on pod",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-hostusers-spec",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							HostUsers: ptr.To(false),
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.HostUsers = ptr.To(false)

				return spec
			},
		},
		{
			name: "host users from policy overrides spec",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-hostusers-policy",
							Namespace: "shared-test",
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
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.HostUsers = ptr.To(false)
				return spec
			},
		},
		{
			name: "worker limit sets container resource limits",
			sharedAgent: SharedAgent{
				Config: &Config{
					cluster: &v1beta1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sc-workerlimit",
							Namespace: "shared-test",
						},
						Spec: v1beta1.ClusterSpec{
							WorkerLimit: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("500m"),
								v1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) v1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
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
			podSpec := tt.sharedAgent.podSpec(context.Background())
			assert.Equal(t, tt.expectedPodSpec(tt.sharedAgent), podSpec)
		})
	}
}

func Test_sharedAgentData(t *testing.T) {
	type args struct {
		cluster     *v1beta1.Cluster
		serviceName string
		ip          string
		kubeletPort int
		token       string
	}

	tests := []struct {
		name         string
		args         args
		expectedData map[string]string
	}{
		{
			name: "simple config",
			args: args{
				cluster: &v1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "mycluster",
						Namespace: "ns-1",
					},
					Spec: v1beta1.ClusterSpec{
						Version: "v1.2.3",
					},
				},
				kubeletPort: 10250,
				ip:          "10.0.0.21",
				serviceName: "service-name",
				token:       "dnjklsdjnksd892389238",
			},
			expectedData: map[string]string{
				"clusterName":      "mycluster",
				"clusterNamespace": "ns-1",
				"serverIP":         "10.0.0.21",
				"serviceName":      "service-name",
				"token":            "dnjklsdjnksd892389238",
				"version":          "v1.2.3",
				"mirrorHostNodes":  "false",
				"kubeletPort":      "10250",
			},
		},
		{
			name: "version in status",
			args: args{
				cluster: &v1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "mycluster",
						Namespace: "ns-1",
					},
					Spec: v1beta1.ClusterSpec{
						Version: "v1.2.3",
					},
					Status: v1beta1.ClusterStatus{
						HostVersion: "v1.3.3",
					},
				},
				ip:          "10.0.0.21",
				kubeletPort: 10250,
				serviceName: "service-name",
				token:       "dnjklsdjnksd892389238",
			},
			expectedData: map[string]string{
				"clusterName":      "mycluster",
				"clusterNamespace": "ns-1",
				"serverIP":         "10.0.0.21",
				"serviceName":      "service-name",
				"token":            "dnjklsdjnksd892389238",
				"version":          "v1.2.3",
				"mirrorHostNodes":  "false",
				"kubeletPort":      "10250",
			},
		},
		{
			name: "missing version in spec",
			args: args{
				cluster: &v1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "mycluster",
						Namespace: "ns-1",
					},
					Status: v1beta1.ClusterStatus{
						HostVersion: "v1.3.3",
					},
				},
				kubeletPort: 10250,
				ip:          "10.0.0.21",
				serviceName: "service-name",
				token:       "dnjklsdjnksd892389238",
			},
			expectedData: map[string]string{
				"clusterName":      "mycluster",
				"clusterNamespace": "ns-1",
				"serverIP":         "10.0.0.21",
				"serviceName":      "service-name",
				"token":            "dnjklsdjnksd892389238",
				"version":          "v1.3.3",
				"mirrorHostNodes":  "false",
				"kubeletPort":      "10250",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := sharedAgentData(tt.args.cluster, tt.args.serviceName, tt.args.token, tt.args.ip, tt.args.kubeletPort)

			data := make(map[string]string)
			err := yaml.Unmarshal([]byte(config), data)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedData, data)
		})
	}
}
