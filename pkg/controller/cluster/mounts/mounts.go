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

func FilterEmptyDirVolumes(podSpec *corev1.PodSpec) {
	// Remove all EmptyDir volumes and their corresponding mounts.
	emptyDirNames := make(map[string]bool)

	var filteredVolumes []corev1.Volume

	for _, vol := range podSpec.Volumes {
		if vol.EmptyDir != nil {
			emptyDirNames[vol.Name] = true
		} else {
			filteredVolumes = append(filteredVolumes, vol)
		}
	}

	podSpec.Volumes = filteredVolumes

	for i := range podSpec.Containers {
		var filteredMounts []corev1.VolumeMount

		for _, mount := range podSpec.Containers[i].VolumeMounts {
			if !emptyDirNames[mount.Name] {
				filteredMounts = append(filteredMounts, mount)
			}
		}

		podSpec.Containers[i].VolumeMounts = filteredMounts
	}
}

func AddKmsgMount(podSpec *corev1.PodSpec) {
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: "dev-kmsg",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: "/dev/kmsg",
			},
		},
	})

	podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      "dev-kmsg",
		MountPath: "/dev/kmsg",
	})
}
