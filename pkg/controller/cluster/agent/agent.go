package agent

import (
	"strings"

	"github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/galal-hussein/k3k/pkg/controller/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Agent(cluster *v1alpha1.Cluster) *apps.Deployment {
	image := util.K3SImage(cluster)

	const name = "k3k-agent"

	return &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-" + name,
			Namespace: util.ClusterNamespace(cluster),
		},
		Spec: apps.DeploymentSpec{
			Replicas: cluster.Spec.Agents,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"cluster": cluster.Name,
					"type":    "agent",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"cluster": cluster.Name,
						"type":    "agent",
					},
				},
				Spec: agentPodSpec(image, name, cluster.Spec.AgentArgs),
			},
		},
	}
}

func agentPodSpec(image, name string, args []string) v1.PodSpec {
	privileged := true

	return v1.PodSpec{
		Volumes: []v1.Volume{
			{
				Name: "config",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: name + "-config",
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
			{
				Name: "varlog",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
		},
		Containers: []v1.Container{
			{
				Name:  name,
				Image: image,
				SecurityContext: &v1.SecurityContext{
					Privileged: &privileged,
				},
				Command: []string{
					"/bin/sh",
				},
				Args: []string{
					"-c",
					"/bin/k3s agent --config /opt/rancher/k3s/config.yaml " +
						strings.Join(args, " ") +
						" && true",
				},
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
}
