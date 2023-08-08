package server

import (
	"strconv"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

const (
	serverName     = "k3k-server"
	initServerName = "k3k-init-server"
)

func Server(cluster *v1alpha1.Cluster, init bool) *apps.Deployment {
	var replicas int32
	image := util.K3SImage(cluster)

	name := serverName
	if init {
		name = initServerName
	}

	replicas = *cluster.Spec.Servers - 1
	if init {
		replicas = 1
	}

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
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"cluster": cluster.Name,
					"role":    "server",
					"init":    strconv.FormatBool(init),
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"cluster": cluster.Name,
						"role":    "server",
						"init":    strconv.FormatBool(init),
					},
				},
				Spec: serverPodSpec(image, name, cluster.Spec.ServerArgs, false),
			},
		},
	}
}

func serverPodSpec(image, name string, args []string, statefulSet bool) v1.PodSpec {
	args = append([]string{"server", "--config", "/opt/rancher/k3s/config.yaml"}, args...)
	podSpec := v1.PodSpec{
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

func StatefulServer(cluster *v1alpha1.Cluster, init bool) *apps.StatefulSet {
	var replicas int32
	image := util.K3SImage(cluster)

	name := serverName
	if init {
		name = initServerName
	}

	replicas = *cluster.Spec.Servers - 1
	if init {
		replicas = 1
	}

	return &apps.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-" + name,
			Namespace: util.ClusterNamespace(cluster),
		},
		Spec: apps.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: cluster.Name + "-" + name + "-headless",
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"cluster": cluster.Name,
					"role":    "server",
					"init":    strconv.FormatBool(init),
				},
			},
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
						StorageClassName: &cluster.Spec.Persistence.StorageClassName,
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								"storage": resource.MustParse(cluster.Spec.Persistence.StorageRequestSize),
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
								"storage": resource.MustParse(cluster.Spec.Persistence.StorageRequestSize),
							},
						},
						AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
						StorageClassName: &cluster.Spec.Persistence.StorageClassName,
					},
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"cluster": cluster.Name,
						"role":    "server",
						"init":    strconv.FormatBool(init),
					},
				},
				Spec: serverPodSpec(image, name, cluster.Spec.ServerArgs, true),
			},
		},
	}
}
