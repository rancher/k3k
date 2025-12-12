package mounts

import (
	"slices"
	"strings"

	"k8s.io/utils/ptr"

	v1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func BuildSecretsMountsVolumes(secretMounts []v1beta1.SecretMount) ([]v1.Volume, []v1.VolumeMount) {
	var (
		vols                []v1.Volume
		volMounts           []v1.VolumeMount
		projectedVolSources []v1.VolumeProjection
	)

	sortedSecretMounts := sortedMounts(secretMounts)

	for _, secretMount := range sortedSecretMounts {
		if secretMount.SecretName == "" || secretMount.MountDirPath == "" {
			continue
		}

		projectedVolSources = []v1.VolumeProjection{
			{
				Secret: &v1.SecretProjection{
					LocalObjectReference: v1.LocalObjectReference{
						Name: secretMount.SecretName,
					},
					Items: secretMount.KeysToPaths,
					//
					Optional: ptr.To(true),
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
			MountPath: secretMount.MountDirPath,
		})
	}

	return vols, volMounts
}

// sortedMounts will return back a sorted list of secretMounts
func sortedMounts(secretMounts []v1beta1.SecretMount) []v1beta1.SecretMount {
	sorted := make([]v1beta1.SecretMount, len(secretMounts))
	copy(sorted, secretMounts)

	slices.SortFunc(sorted, func(a, b v1beta1.SecretMount) int {
		return strings.Compare(a.SecretName, b.SecretName)
	})

	return sorted
}
