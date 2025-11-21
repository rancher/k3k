package registry

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func BuildRegistryConfigVolumes(ctx context.Context, cluster *v1beta1.Cluster, client client.Client) ([]v1.Volume, []v1.VolumeMount, error) {
	var (
		vols      []v1.Volume
		volMounts []v1.VolumeMount
	)
	vols = append(vols, v1.Volume{
		Name: "private-registries",
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: cluster.Spec.PrivateRegistry.SecretName,
				Items: []v1.KeyToPath{
					{
						Key:  "registries.yaml",
						Path: "registries.yaml",
					},
				},
			},
		},
	})
	volMounts = append(volMounts, v1.VolumeMount{
		Name:      "private-registries",
		MountPath: "/opt/rancher/k3s/registry",
	})

	// use projected volume to mount multiple secrets in the same dir
	tlsSecretsMap := cluster.Spec.PrivateRegistry.TLSSecrets
	if len(tlsSecretsMap) > 0 {
		var projectedVolSources []v1.VolumeProjection
		sortedTLSKeys := sortedKeys(tlsSecretsMap)
		for _, tlsSecret := range sortedTLSKeys {
			var privRegistrySecret v1.Secret
			secretKey := types.NamespacedName{
				Name:      tlsSecretsMap[tlsSecret].SecretName,
				Namespace: cluster.Namespace,
			}

			if err := client.Get(ctx, secretKey, &privRegistrySecret); err != nil {
				return nil, nil, err
			}

			if privRegistrySecret.Type != v1.SecretTypeTLS {
				return nil, nil, fmt.Errorf("TLS secret specified for private registry is not TLS type")
			}
			projectedVolSources = append(projectedVolSources, v1.VolumeProjection{
				Secret: &v1.SecretProjection{
					LocalObjectReference: v1.LocalObjectReference{
						Name: tlsSecretsMap[tlsSecret].SecretName,
					},
					Items: []v1.KeyToPath{
						{
							Key:  "tls.crt",
							Path: tlsSecret + ".crt",
						},
						{
							Key:  "tls.key",
							Path: tlsSecret + ".key",
						},
					},
				},
			})
		}
		vols = append(vols, v1.Volume{
			Name: "tls-vol",
			VolumeSource: v1.VolumeSource{
				Projected: &v1.ProjectedVolumeSource{
					Sources: projectedVolSources,
				},
			},
		})

		volMounts = append(volMounts, v1.VolumeMount{
			Name:      "tls-vol",
			MountPath: "/opt/rancher/k3s/registry/ssl",
		})
	}
	return vols, volMounts, nil
}

func sortedKeys(keyMap map[string]v1beta1.CredentialSource) []string {
	keys := make([]string, 0, len(keyMap))

	for k := range keyMap {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
