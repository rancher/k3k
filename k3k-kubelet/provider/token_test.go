package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	k3kcontroller "github.com/rancher/k3k/pkg/controller"
)

func Test_isKubeAccessVolumeFound(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "no volumes",
			pod:  &corev1.Pod{},
			want: false,
		},
		{
			name: "volume with kube-api-access prefix",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "kube-api-access-abc123"},
					},
				},
			},
			want: true,
		},
		{
			name: "exact kube-api-access name",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "kube-api-access"},
					},
				},
			},
			want: true,
		},
		{
			name: "volume without kube-api-access prefix",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "my-volume"},
					},
				},
			},
			want: false,
		},
		{
			name: "multiple volumes with one kube-api-access",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "config-volume"},
						{Name: "kube-api-access-xyz"},
						{Name: "data-volume"},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isKubeAccessVolumeFound(tt.pod))
		})
	}
}

func Test_removeKubeAccessVolume(t *testing.T) {
	t.Run("removes volume and all volume mounts from containers", func(t *testing.T) {
		pod := &corev1.Pod{
			Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{
					{Name: "config-volume"},
					{Name: "kube-api-access-abc"},
					{Name: "data-volume"},
				},
				InitContainers: []corev1.Container{
					{
						Name: "init",
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config-volume", MountPath: "/config"},
							{Name: "kube-api-access-abc", MountPath: serviceAccountTokenMountPath},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name: "main",
						VolumeMounts: []corev1.VolumeMount{
							{Name: "kube-api-access-abc", MountPath: serviceAccountTokenMountPath},
							{Name: "data-volume", MountPath: "/data"},
						},
					},
				},
				EphemeralContainers: []corev1.EphemeralContainer{
					{
						EphemeralContainerCommon: corev1.EphemeralContainerCommon{
							Name: "debug",
							VolumeMounts: []corev1.VolumeMount{
								{Name: "kube-api-access-abc", MountPath: serviceAccountTokenMountPath},
							},
						},
					},
				},
			},
		}

		removeKubeAccessVolume(pod)

		// Verify volume was removed
		assert.Equal(t, 2, len(pod.Spec.Volumes))
		assert.Equal(t, "config-volume", pod.Spec.Volumes[0].Name)
		assert.Equal(t, "data-volume", pod.Spec.Volumes[1].Name)

		// Verify init container mount was removed
		assert.Equal(t, 1, len(pod.Spec.InitContainers[0].VolumeMounts))
		assert.Equal(t, "config-volume", pod.Spec.InitContainers[0].VolumeMounts[0].Name)

		// Verify container mount was removed
		assert.Equal(t, 1, len(pod.Spec.Containers[0].VolumeMounts))
		assert.Equal(t, "data-volume", pod.Spec.Containers[0].VolumeMounts[0].Name)

		// Verify ephemeral container mount was removed
		assert.Equal(t, 0, len(pod.Spec.EphemeralContainers[0].VolumeMounts))
	})

	t.Run("no kube-api-access volume present", func(t *testing.T) {
		pod := &corev1.Pod{
			Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{
					{Name: "config-volume"},
				},
				Containers: []corev1.Container{
					{
						Name: "main",
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config-volume", MountPath: "/config"},
						},
					},
				},
			},
		}

		removeKubeAccessVolume(pod)

		assert.Equal(t, 1, len(pod.Spec.Volumes))
		assert.Equal(t, "config-volume", pod.Spec.Volumes[0].Name)
		assert.Equal(t, 1, len(pod.Spec.Containers[0].VolumeMounts))
	})
}

func Test_addKubeAccessVolume(t *testing.T) {
	tokenVolumeName := k3kcontroller.SafeConcatNameWithPrefix(kubeAPIAccessPrefix)
	hostSecretName := "host-secret-token"

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{Name: "existing-volume"},
			},
			InitContainers: []corev1.Container{
				{Name: "init"},
			},
			Containers: []corev1.Container{
				{Name: "main"},
				{Name: "sidecar"},
			},
			EphemeralContainers: []corev1.EphemeralContainer{
				{
					EphemeralContainerCommon: corev1.EphemeralContainerCommon{
						Name: "debug",
					},
				},
			},
		},
	}

	addKubeAccessVolume(pod, hostSecretName)

	// Verify volume was added
	assert.Equal(t, 2, len(pod.Spec.Volumes))
	addedVol := pod.Spec.Volumes[1]
	assert.Equal(t, tokenVolumeName, addedVol.Name)
	assert.Equal(t, hostSecretName, addedVol.VolumeSource.Secret.SecretName)

	// Verify init container mount was added
	assert.Equal(t, 1, len(pod.Spec.InitContainers[0].VolumeMounts))
	assert.Equal(t, tokenVolumeName, pod.Spec.InitContainers[0].VolumeMounts[0].Name)
	assert.Equal(t, serviceAccountTokenMountPath, pod.Spec.InitContainers[0].VolumeMounts[0].MountPath)

	// Verify all container mounts were added
	for _, c := range pod.Spec.Containers {
		assert.Equal(t, 1, len(c.VolumeMounts), "container %s should have mount", c.Name)
		assert.Equal(t, tokenVolumeName, c.VolumeMounts[0].Name)
		assert.Equal(t, serviceAccountTokenMountPath, c.VolumeMounts[0].MountPath)
	}

	// Verify ephemeral container mounts were added
	for _, c := range pod.Spec.EphemeralContainers {
		assert.Equal(t, 1, len(c.VolumeMounts), "ephemeral container %s should have mount", c.Name)
		assert.Equal(t, tokenVolumeName, c.VolumeMounts[0].Name)
		assert.Equal(t, serviceAccountTokenMountPath, c.VolumeMounts[0].MountPath)
	}
}

func Test_virtualSecret(t *testing.T) {
	s := virtualSecret("my-secret", "my-ns", "my-sa")

	assert.Equal(t, "my-secret", s.Name)
	assert.Equal(t, "my-ns", s.Namespace)
	assert.Equal(t, corev1.SecretTypeServiceAccountToken, s.Type)
	assert.Equal(t, "my-sa", s.Annotations[corev1.ServiceAccountNameKey])
	assert.Equal(t, "Secret", s.Kind)
	assert.Equal(t, "v1", s.APIVersion)
}

func Test_generateTokenSecretName(t *testing.T) {
	tests := []struct {
		name               string
		serviceAccountName string
		tokenReq           *authv1.TokenRequest
		want               string
	}{
		{
			name:               "no audiences, no expiration",
			serviceAccountName: "default",
			tokenReq: &authv1.TokenRequest{
				Spec: authv1.TokenRequestSpec{},
			},
			want: "k3k-default",
		},
		{
			name:               "no audiences, with expiration",
			serviceAccountName: "default",
			tokenReq: &authv1.TokenRequest{
				Spec: authv1.TokenRequestSpec{
					ExpirationSeconds: ptr.To(int64(3600)),
				},
			},
			want: "k3k-default-3600",
		},
		{
			name:               "with single audience and expiration",
			serviceAccountName: "my-sa",
			tokenReq: &authv1.TokenRequest{
				Spec: authv1.TokenRequestSpec{
					Audiences:         []string{"api"},
					ExpirationSeconds: ptr.To(int64(3600)),
				},
			},
			want: "k3k-my-sa-api-3600",
		},
		{
			name:               "with multiple audiences and expiration",
			serviceAccountName: "my-sa",
			tokenReq: &authv1.TokenRequest{
				Spec: authv1.TokenRequestSpec{
					Audiences:         []string{"api", "vault"},
					ExpirationSeconds: ptr.To(int64(3600)),
				},
			},
			want: "k3k-my-sa-api-vault-3600",
		},
		{
			name:               "with audiences, no expiration",
			serviceAccountName: "my-sa",
			tokenReq: &authv1.TokenRequest{
				Spec: authv1.TokenRequestSpec{
					Audiences: []string{"api"},
				},
			},
			want: "k3k-my-sa-api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, generateTokenSecretName(tt.serviceAccountName, tt.tokenReq))
		})
	}
}
