package mounts

import (
	"testing"

	"github.com/stretchr/testify/assert"

	v1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_BuildSecretMountsVolume(t *testing.T) {
	type args struct {
		secretMounts []v1beta1.SecretMount
		role         string
	}

	type expectedVolumes struct {
		vols      []v1.Volume
		volMounts []v1.VolumeMount
	}

	tests := []struct {
		name         string
		args         args
		expectedData expectedVolumes
	}{
		{
			name: "empty secret mounts",
			args: args{
				secretMounts: []v1beta1.SecretMount{},
				role:         "server",
			},
			expectedData: expectedVolumes{
				vols:      nil,
				volMounts: nil,
			},
		},
		{
			name: "nil secret mounts",
			args: args{
				secretMounts: nil,
				role:         "server",
			},
			expectedData: expectedVolumes{
				vols:      nil,
				volMounts: nil,
			},
		},
		{
			name: "single secret mount with no role specified defaults to all",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-1",
						},
						MountPath: "/mount-dir-1",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("secret-1", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("secret-1", "/mount-dir-1", ""),
				},
			},
		},
		{
			name: "multiple secrets mounts with no role specified defaults to all",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-1",
						},
						MountPath: "/mount-dir-1",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-2",
						},
						MountPath: "/mount-dir-2",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("secret-1", nil),
					expectedVolume("secret-2", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("secret-1", "/mount-dir-1", ""),
					expectedVolumeMount("secret-2", "/mount-dir-2", ""),
				},
			},
		},
		{
			name: "single secret mount with items",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-1",
							Items: []v1.KeyToPath{
								{
									Key:  "key-1",
									Path: "path-1",
								},
							},
						},
						MountPath: "/mount-dir-1",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("secret-1", []v1.KeyToPath{{Key: "key-1", Path: "path-1"}}),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("secret-1", "/mount-dir-1", ""),
				},
			},
		},
		{
			name: "multiple secret mounts with items",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-1",
							Items: []v1.KeyToPath{
								{
									Key:  "key-1",
									Path: "path-1",
								},
							},
						},
						MountPath: "/mount-dir-1",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-2",
							Items: []v1.KeyToPath{
								{
									Key:  "key-2",
									Path: "path-2",
								},
							},
						},
						MountPath: "/mount-dir-2",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("secret-1", []v1.KeyToPath{{Key: "key-1", Path: "path-1"}}),
					expectedVolume("secret-2", []v1.KeyToPath{{Key: "key-2", Path: "path-2"}}),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("secret-1", "/mount-dir-1", ""),
					expectedVolumeMount("secret-2", "/mount-dir-2", ""),
				},
			},
		},
		{
			name: "user will specify the order",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "z-secret",
						},
						MountPath: "/z",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "a-secret",
						},
						MountPath: "/a",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "m-secret",
						},
						MountPath: "/m",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("z-secret", nil),
					expectedVolume("a-secret", nil),
					expectedVolume("m-secret", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("z-secret", "/z", ""),
					expectedVolumeMount("a-secret", "/a", ""),
					expectedVolumeMount("m-secret", "/m", ""),
				},
			},
		},
		{
			name: "skip entries with empty secret name",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						MountPath: "/mount-dir-1",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-2",
						},
						MountPath: "/mount-dir-2",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("secret-2", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("secret-2", "/mount-dir-2", ""),
				},
			},
		},
		{
			name: "skip entries with empty mount path",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-1",
						},
						MountPath: "",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-2",
						},
						MountPath: "/mount-dir-2",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("secret-2", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("secret-2", "/mount-dir-2", ""),
				},
			},
		},
		{
			name: "secret mount with subPath",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-1",
						},
						MountPath: "/etc/rancher/k3s/registries.yaml",
						SubPath:   "registries.yaml",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("secret-1", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("secret-1", "/etc/rancher/k3s/registries.yaml", "registries.yaml"),
				},
			},
		},
		// Role-based filtering tests
		{
			name: "role server includes only server and all roles",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "server-secret",
						},
						MountPath: "/server",
						Role:      "server",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "agent-secret",
						},
						MountPath: "/agent",
						Role:      "agent",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "all-secret",
						},
						MountPath: "/all",
						Role:      "all",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("server-secret", nil),
					expectedVolume("all-secret", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("server-secret", "/server", ""),
					expectedVolumeMount("all-secret", "/all", ""),
				},
			},
		},
		{
			name: "role agent includes only agent and all roles",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "server-secret",
						},
						MountPath: "/server",
						Role:      "server",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "agent-secret",
						},
						MountPath: "/agent",
						Role:      "agent",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "all-secret",
						},
						MountPath: "/all",
						Role:      "all",
					},
				},
				role: "agent",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("agent-secret", nil),
					expectedVolume("all-secret", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("agent-secret", "/agent", ""),
					expectedVolumeMount("all-secret", "/all", ""),
				},
			},
		},
		{
			name: "empty role in secret mount defaults to all",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "no-role-secret",
						},
						MountPath: "/no-role",
						Role:      "",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "server-secret",
						},
						MountPath: "/server",
						Role:      "server",
					},
				},
				role: "agent",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("no-role-secret", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("no-role-secret", "/no-role", ""),
				},
			},
		},
		{
			name: "mixed roles with server filter",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "registry-config",
						},
						MountPath: "/etc/rancher/k3s/registries.yaml",
						SubPath:   "registries.yaml",
						Role:      "all",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "server-config",
						},
						MountPath: "/etc/server",
						Role:      "server",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "agent-config",
						},
						MountPath: "/etc/agent",
						Role:      "agent",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("registry-config", nil),
					expectedVolume("server-config", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("registry-config", "/etc/rancher/k3s/registries.yaml", "registries.yaml"),
					expectedVolumeMount("server-config", "/etc/server", ""),
				},
			},
		},
		{
			name: "all secrets have role all",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-1",
						},
						MountPath: "/secret-1",
						Role:      "all",
					},
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-2",
						},
						MountPath: "/secret-2",
						Role:      "all",
					},
				},
				role: "server",
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("secret-1", nil),
					expectedVolume("secret-2", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("secret-1", "/secret-1", ""),
					expectedVolumeMount("secret-2", "/secret-2", ""),
				},
			},
		},
		{
			name: "no secrets match agent role",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "server-only",
						},
						MountPath: "/server-only",
						Role:      "server",
					},
				},
				role: "agent",
			},
			expectedData: expectedVolumes{
				vols:      nil,
				volMounts: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vols, volMounts := BuildSecretsMountsVolumes(tt.args.secretMounts, tt.args.role)

			assert.Equal(t, tt.expectedData.vols, vols)
			assert.Equal(t, tt.expectedData.volMounts, volMounts)
		})
	}
}

func expectedVolume(name string, items []v1.KeyToPath) v1.Volume {
	return v1.Volume{
		Name: name,
		VolumeSource: v1.VolumeSource{
			Projected: &v1.ProjectedVolumeSource{
				Sources: []v1.VolumeProjection{
					{Secret: &v1.SecretProjection{
						LocalObjectReference: v1.LocalObjectReference{
							Name: name,
						},
						Items: items,
					}},
				},
			},
		},
	}
}

func expectedVolumeMount(name, mountPath, subPath string) v1.VolumeMount {
	return v1.VolumeMount{
		Name:      name,
		MountPath: mountPath,
		SubPath:   subPath,
	}
}
