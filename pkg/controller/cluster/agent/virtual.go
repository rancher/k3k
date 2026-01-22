package agent

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/utils/ptr"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/mounts"
)

const (
	VirtualNodeMode      = "virtual"
	virtualNodeAgentName = "agent"
)

type VirtualAgent struct {
	*Config
	serviceIP        string
	token            string
	Image            string
	ImagePullPolicy  string
	ImageRegistry    string
	imagePullSecrets []string
}

func NewVirtualAgent(config *Config, serviceIP, token, Image, ImagePullPolicy string, imagePullSecrets []string) *VirtualAgent {
	return &VirtualAgent{
		Config:           config,
		serviceIP:        serviceIP,
		token:            token,
		Image:            Image,
		ImagePullPolicy:  ImagePullPolicy,
		imagePullSecrets: imagePullSecrets,
	}
}

func (v *VirtualAgent) Name() string {
	return controller.SafeConcatNameWithPrefix(v.cluster.Name, virtualNodeAgentName)
}

func (v *VirtualAgent) EnsureResources(ctx context.Context) error {
	if err := errors.Join(
		v.config(ctx),
		v.deployment(ctx),
	); err != nil {
		return fmt.Errorf("failed to ensure some resources: %w", err)
	}

	return nil
}

func (v *VirtualAgent) ensureObject(ctx context.Context, obj ctrlruntimeclient.Object) error {
	return ensureObject(ctx, v.Config, obj)
}

func (v *VirtualAgent) config(ctx context.Context) error {
	config := virtualAgentData(v.serviceIP, v.token)

	configSecret := &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      configSecretName(v.cluster.Name),
			Namespace: v.cluster.Namespace,
		},
		Data: map[string][]byte{
			"config.yaml": []byte(config),
		},
	}

	return v.ensureObject(ctx, configSecret)
}

func virtualAgentData(serviceIP, token string) string {
	return fmt.Sprintf(`server: https://%s
token: %s
with-node-id: true`, serviceIP, token)
}

func (v *VirtualAgent) deployment(ctx context.Context) error {
	image := controller.K3SImage(v.cluster, v.Image)

	const name = "k3k-agent"

	selector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"cluster": v.cluster.Name,
			"type":    "agent",
			"mode":    "virtual",
		},
	}
	podSpec := v.podSpec(image, name)

	if len(v.cluster.Spec.SecretMounts) > 0 {
		vols, volMounts := mounts.BuildSecretsMountsVolumes(v.cluster.Spec.SecretMounts)

		podSpec.Volumes = append(podSpec.Volumes, vols...)

		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, volMounts...)
	}

	deployment := &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.Name(),
			Namespace: v.cluster.Namespace,
			Labels:    selector.MatchLabels,
		},
		Spec: apps.DeploymentSpec{
			Replicas: v.cluster.Spec.Agents,
			Selector: &selector,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector.MatchLabels,
				},
				Spec: podSpec,
			},
		},
	}

	return v.ensureObject(ctx, deployment)
}

func (v *VirtualAgent) podSpec(image, name string) v1.PodSpec {
	var limit v1.ResourceList

	args := v.cluster.Spec.AgentArgs
	args = append([]string{"agent", "--config", "/opt/rancher/k3s/config.yaml"}, args...)

	podSpec := v1.PodSpec{
		NodeSelector: v.cluster.Spec.NodeSelector,
		Volumes: []v1.Volume{
			{
				Name: "config",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: configSecretName(v.cluster.Name),
						Items: []v1.KeyToPath{
							{
								Key:  "config.yaml",
								Path: "config.yaml",
							},
						},
					},
				},
			},
			{
				Name: "run",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varrun",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibcni",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlog",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibkubelet",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibrancherk3s",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
		},
		Containers: []v1.Container{
			{
				Name:            name,
				Image:           image,
				ImagePullPolicy: v1.PullPolicy(v.ImagePullPolicy),
				SecurityContext: &v1.SecurityContext{
					Privileged: ptr.To(true),
				},
				Args: args,
				Command: []string{
					"/bin/k3s",
				},
				Resources: v1.ResourceRequirements{
					Limits: limit,
				},
				Env: v.cluster.Spec.AgentEnvs,
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      "config",
						MountPath: "/opt/rancher/k3s/",
						ReadOnly:  false,
					},
					{
						Name:      "run",
						MountPath: "/run",
						ReadOnly:  false,
					},
					{
						Name:      "varrun",
						MountPath: "/var/run",
						ReadOnly:  false,
					},
					{
						Name:      "varlibcni",
						MountPath: "/var/lib/cni",
						ReadOnly:  false,
					},
					{
						Name:      "varlibkubelet",
						MountPath: "/var/lib/kubelet",
						ReadOnly:  false,
					},
					{
						Name:      "varlibrancherk3s",
						MountPath: "/var/lib/rancher/k3s",
						ReadOnly:  false,
					},
					{
						Name:      "varlog",
						MountPath: "/var/log",
						ReadOnly:  false,
					},
				},
			},
		},
	}

	// specify resource limits if specified for the servers.
	if v.cluster.Spec.WorkerLimit != nil {
		podSpec.Containers[0].Resources = v1.ResourceRequirements{
			Limits: v.cluster.Spec.WorkerLimit,
		}
	}

	for _, imagePullSecret := range v.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, v1.LocalObjectReference{Name: imagePullSecret})
	}

	return podSpec
}
