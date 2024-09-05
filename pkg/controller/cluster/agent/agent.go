package agent

import (
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

const agentName = "k3k-agent"

type Agent struct {
	cluster *v1alpha1.Cluster
}

func New(cluster *v1alpha1.Cluster) *Agent {
	return &Agent{
		cluster: cluster,
	}
}

func (a *Agent) Deploy() *apps.Deployment {
	image := util.K3SImage(a.cluster)

	const name = "k3k-agent"
	selector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"cluster": a.cluster.Name,
			"type":    "agent",
		},
	}
	return &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.cluster.Name + "-" + name,
			Namespace: util.ClusterNamespace(a.cluster),
			Labels:    selector.MatchLabels,
		},
		Spec: apps.DeploymentSpec{
			Replicas: a.cluster.Spec.Agents,
			Selector: &selector,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector.MatchLabels,
				},
				Spec: a.podSpec(image, name, a.cluster.Spec.AgentArgs, false, &selector),
			},
		},
	}
}

func (a *Agent) StatefulAgent(cluster *v1alpha1.Cluster) *apps.StatefulSet {
	image := util.K3SImage(cluster)

	selector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"cluster": cluster.Name,
			"type":    "agent",
		},
	}
	return &apps.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Statefulset",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-" + agentName,
			Namespace: util.ClusterNamespace(cluster),
			Labels:    selector.MatchLabels,
		},
		Spec: apps.StatefulSetSpec{
			ServiceName: cluster.Name + "-" + agentName + "-headless",
			Replicas:    cluster.Spec.Agents,
			Selector:    &selector,
			VolumeClaimTemplates: []v1.PersistentVolumeClaim{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       "PersistentVolumeClaim",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "varlibrancherk3s",
						Namespace: util.ClusterNamespace(cluster),
					},
					Spec: v1.PersistentVolumeClaimSpec{
						AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
						StorageClassName: &cluster.Status.Persistence.StorageClassName,
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								"storage": resource.MustParse(cluster.Status.Persistence.StorageRequestSize),
							},
						},
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       "PersistentVolumeClaim",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "varlibkubelet",
						Namespace: util.ClusterNamespace(cluster),
					},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								"storage": resource.MustParse(cluster.Status.Persistence.StorageRequestSize),
							},
						},
						AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
						StorageClassName: &cluster.Status.Persistence.StorageClassName,
					},
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector.MatchLabels,
				},
				Spec: a.podSpec(image, agentName, cluster.Spec.AgentArgs, true, &selector),
			},
		},
	}
}

func (a *Agent) podSpec(image, name string, args []string, statefulSet bool, affinitySelector *metav1.LabelSelector) v1.PodSpec {
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
					Privileged: pointer.Bool(true),
				},
				Command: []string{
					"/bin/k3s",
				},
				Args: args,
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

	if !statefulSet {
		podSpec.Volumes = append(podSpec.Volumes, v1.Volume{
			Name: "varlibkubelet",
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{},
			},
		}, v1.Volume{

			Name: "varlibrancherk3s",
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{},
			},
		},
		)
	}

	return podSpec
}
