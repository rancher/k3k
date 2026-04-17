package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/resource"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func baseSharedAgentPodSpec(sharedAgent SharedAgent) corev1.PodSpec {
	return corev1.PodSpec{
		Affinity:           nil,
		HostNetwork:        false,
		DNSPolicy:          corev1.DNSClusterFirst,
		ServiceAccountName: sharedAgent.Name(),
		NodeSelector:       nil,
		Volumes: []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: configSecretName(sharedAgent.cluster.Name),
						Items: []corev1.KeyToPath{
							{
								Key:  "config.yaml",
								Path: "config.yaml",
							},
						},
					},
				},
			},
		},
		Containers: []corev1.Container{
			{
				Name:            sharedAgent.Name(),
				Image:           sharedAgent.image,
				ImagePullPolicy: corev1.PullPolicy(sharedAgent.imagePullPolicy),
				Env: []corev1.EnvVar{
					{
						Name: "AGENT_HOSTNAME",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "spec.nodeName",
							},
						},
					},
					{
						Name: "POD_IP",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "status.podIP",
							},
						},
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "config",
						MountPath: "/opt/rancher/k3k/",
						ReadOnly:  false,
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "kubelet-port",
						Protocol:      corev1.ProtocolTCP,
						ContainerPort: int32(sharedAgent.kubeletPort),
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
		expectedPodSpec func(SharedAgent) corev1.PodSpec
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
			expectedPodSpec: func(sa SharedAgent) corev1.PodSpec {
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
			expectedPodSpec: func(sa SharedAgent) corev1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.HostNetwork = true
				spec.DNSPolicy = corev1.DNSClusterFirstWithHostNet

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
			expectedPodSpec: func(sa SharedAgent) corev1.PodSpec {
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
			expectedPodSpec: func(sa SharedAgent) corev1.PodSpec {
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
							AgentEnvs: []corev1.EnvVar{
								{Name: "CUSTOM_VAR", Value: "custom-value"},
								{Name: "ANOTHER_VAR", Value: "another-value"},
							},
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) corev1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.Containers[0].Env = append(spec.Containers[0].Env,
					corev1.EnvVar{Name: "CUSTOM_VAR", Value: "custom-value"},
					corev1.EnvVar{Name: "ANOTHER_VAR", Value: "another-value"},
				)

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
			expectedPodSpec: func(sa SharedAgent) corev1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.ImagePullSecrets = []corev1.LocalObjectReference{
					{Name: "secret-1"},
					{Name: "secret-2"},
				}

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
							WorkerLimit: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					},
				},
				image:       "rancher/k3k-kubelet:latest",
				kubeletPort: 10250,
			},
			expectedPodSpec: func(sa SharedAgent) corev1.PodSpec {
				spec := baseSharedAgentPodSpec(sa)
				spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				}

				return spec
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			podSpec := tt.sharedAgent.podSpec()
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
		webhookPort int
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
				webhookPort: 9443,
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
				"webhookPort":      "9443",
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
				webhookPort: 9443,
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
				"webhookPort":      "9443",
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
				webhookPort: 9443,
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
				"webhookPort":      "9443",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := sharedAgentData(tt.args.cluster, tt.args.serviceName, tt.args.token, tt.args.ip, tt.args.kubeletPort, tt.args.webhookPort)

			data := make(map[string]string)
			err := yaml.Unmarshal([]byte(config), data)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedData, data)
		})
	}
}
