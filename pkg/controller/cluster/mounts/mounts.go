package mounts

import (
	v1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func BuildSecretsMountsVolumes(secretMounts []v1beta1.SecretMount) ([]v1.Volume, []v1.VolumeMount) {
	var (
		vols      []v1.Volume
		volMounts []v1.VolumeMount
	)

	for _, secretMount := range secretMounts {
		if secretMount.SecretName == "" || secretMount.MountPath == "" {
			continue
		}

		projectedVolSources := []v1.VolumeProjection{
			{
				Secret: &v1.SecretProjection{
					LocalObjectReference: v1.LocalObjectReference{
						Name: secretMount.SecretName,
					},
					Items:    secretMount.Items,
					Optional: secretMount.Optional,
				},
			},
		}

		vols = append(vols, v1.Volume{
			Name: secretMount.SecretName,
			VolumeSource: v1.VolumeSource{
				Projected: &v1.ProjectedVolumeSource{
					Sources: projectedVolSources,
				},
			},
		})

		volMounts = append(volMounts, v1.VolumeMount{
			Name:      secretMount.SecretName,
			MountPath: secretMount.MountPath,
			SubPath:   secretMount.SubPath,
		})
	}

	return vols, volMounts
}
