package agent

import (
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	VirtualNodeMode      = "virtual"
	virtualNodeAgentName = "agent"
)

type VirtualAgent struct {
	cluster   *v1alpha1.Cluster
	serviceIP string
	token     string
}

func NewVirtualAgent(cluster *v1alpha1.Cluster, serviceIP, token string) Agent {
	return &VirtualAgent{
		cluster:   cluster,
		serviceIP: serviceIP,
		token:     token,
	}
}

func (v *VirtualAgent) Config() (ctrlruntimeclient.Object, error) {
	config := virtualAgentData(v.serviceIP, v.token)

	return &v1.Secret{
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
	}, nil
}

func (v *VirtualAgent) Resources() []ctrlruntimeclient.Object {
	return []ctrlruntimeclient.Object{v.deployment()}
}

func virtualAgentData(serviceIP, token string) string {
	return fmt.Sprintf(`server: https://%s:6443
token: %s
with-node-id: true`, serviceIP, token)
}

func (v *VirtualAgent) deployment() *apps.Deployment {
	image := controller.K3SImage(v.cluster)

	const name = "k3k-agent"
	selector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"cluster": v.cluster.Name,
			"type":    "agent",
			"mode":    "virtual",
		},
	}
	return &apps.Deployment{
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
				Spec: v.podSpec(image, name, v.cluster.Spec.AgentArgs, &selector),
			},
		},
	}
}

func (v *VirtualAgent) podSpec(image, name string, args []string, affinitySelector *metav1.LabelSelector) v1.PodSpec {
	var limit v1.ResourceList
	args = append([]string{"agent", "--config", "/opt/rancher/k3s/config.yaml"}, args...)
	podSpec := v1.PodSpec{
		Affinity: &v1.Affinity{
			PodAntiAffinity: &v1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
					{
						LabelSelector: affinitySelector,
						TopologyKey:   "kubernetes.io/hostname",
					},
				},
			},
		},
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
				ImagePullPolicy: v1.PullAlways,
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

	return podSpec
}

func (v *VirtualAgent) Name() string {
	return controller.SafeConcatNameWithPrefix(v.cluster.Name, virtualNodeAgentName)
}
