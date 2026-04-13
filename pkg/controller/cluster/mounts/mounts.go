package mounts

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func BuildSecretsMountsVolumes(secretMounts []v1beta1.SecretMount, role string) ([]corev1.Volume, []corev1.VolumeMount) {
	var (
		vols      []corev1.Volume
		volMounts []corev1.VolumeMount
	)

	for _, secretMount := range secretMounts {
		if secretMount.SecretName == "" || secretMount.MountPath == "" {
			continue
		}

		if secretMount.Role == role || secretMount.Role == "" || secretMount.Role == "all" {
			vol, volMount := buildSecretMountVolume(secretMount)

			vols = append(vols, vol)
			volMounts = append(volMounts, volMount)
		}
	}

	return vols, volMounts
}

func buildSecretMountVolume(secretMount v1beta1.SecretMount) (corev1.Volume, corev1.VolumeMount) {
	projectedVolSources := []corev1.VolumeProjection{
		{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretMount.SecretName,
				},
				Items:    secretMount.Items,
				Optional: secretMount.Optional,
			},
		},
	}

	vol := corev1.Volume{
		Name: secretMount.SecretName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				Sources: projectedVolSources,
			},
		},
	}

	volMount := corev1.VolumeMount{
		Name:      secretMount.SecretName,
		MountPath: secretMount.MountPath,
		SubPath:   secretMount.SubPath,
	}

	return vol, volMount
}
