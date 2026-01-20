package mounts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

	v1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_BuildSecretMountsVolume(t *testing.T) {
	type args struct {
		secretMounts []v1beta1.SecretMount
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
			},
			expectedData: expectedVolumes{
				vols:      nil,
				volMounts: nil,
			},
		},
		{
			name: "single secret mount",
			args: args{
				secretMounts: []v1beta1.SecretMount{
					{
						SecretVolumeSource: v1.SecretVolumeSource{
							SecretName: "secret-1",
						},
						MountPath: "/mount-dir-1",
					},
				},
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
			name: "multiple secrets mounts",
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
			name: "secrets are sorted by name",
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
			},
			expectedData: expectedVolumes{
				vols: []v1.Volume{
					expectedVolume("a-secret", nil),
					expectedVolume("m-secret", nil),
					expectedVolume("z-secret", nil),
				},
				volMounts: []v1.VolumeMount{
					expectedVolumeMount("a-secret", "/a", ""),
					expectedVolumeMount("m-secret", "/m", ""),
					expectedVolumeMount("z-secret", "/z", ""),
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vols, volMounts := BuildSecretsMountsVolumes(tt.args.secretMounts)

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
						Items:    items,
						Optional: ptr.To(true),
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
