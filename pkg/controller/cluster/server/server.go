package server

import (
	"context"
	"strings"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	serverName         = "k3k-"
	k3kSystemNamespace = serverName + "system"
	initServerName     = serverName + "init-server"
	initContainerName  = serverName + "server-check"
	initContainerImage = "alpine/curl"

	EphermalNodesType = "ephermal"
	DynamicNodesType  = "dynamic"
)

// Server
type Server struct {
	cluster *v1alpha1.Cluster
	client  client.Client
}

func New(cluster *v1alpha1.Cluster, client client.Client) *Server {
	return &Server{
		cluster: cluster,
		client:  client,
	}
}

func (s *Server) podSpec(ctx context.Context, image, name string, persistent bool) v1.PodSpec {
	podSpec := v1.PodSpec{
		Volumes: []v1.Volume{
			{
				Name: "initconfig",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: "k3k-init-server-config",
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
				Name: "config",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: "k3k-server-config",
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
				Env: []v1.EnvVar{
					{
						Name: "POD_NAME",
						ValueFrom: &v1.EnvVarSource{
							FieldRef: &v1.ObjectFieldSelector{
								FieldPath: "metadata.name",
							},
						},
					},
				},
				SecurityContext: &v1.SecurityContext{
					Privileged: pointer.Bool(true),
				},
				Command: []string{
					"/bin/sh",
					"-c",
					`if [ ${POD_NAME: -1} == 0 ]; then 
				       /bin/k3s server --config /opt/rancher/k3s/init/config.yaml ` + strings.Join(s.cluster.Spec.ServerArgs, " ") + `
					else /bin/k3s server --config /opt/rancher/k3s/server/config.yaml ` + strings.Join(s.cluster.Spec.ServerArgs, " ") + `
					fi   
					`,
				},
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      "config",
						MountPath: "/opt/rancher/k3s/server",
						ReadOnly:  false,
					},
					{
						Name:      "initconfig",
						MountPath: "/opt/rancher/k3s/init",
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

	if !persistent {
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

	// Adding readiness probes to deployment
	podSpec.Containers[0].ReadinessProbe = &v1.Probe{
		InitialDelaySeconds: 60,
		FailureThreshold:    5,
		TimeoutSeconds:      10,
		ProbeHandler: v1.ProbeHandler{
			TCPSocket: &v1.TCPSocketAction{
				Port: intstr.FromInt(6443),
				Host: "127.0.0.1",
			},
		},
	}

	return podSpec
}

func (s *Server) StatefulServer(ctx context.Context, cluster *v1alpha1.Cluster) (*apps.StatefulSet, error) {
	var (
		replicas   int32
		pvClaims   []v1.PersistentVolumeClaim
		persistent bool
	)
	image := util.K3SImage(cluster)
	name := serverName + "server"

	replicas = *cluster.Spec.Servers

	if cluster.Spec.Persistence != nil && cluster.Spec.Persistence.Type != EphermalNodesType {
		persistent = true
		pvClaims = []v1.PersistentVolumeClaim{
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
		}
	}

	var volumes []v1.Volume
	var volumeMounts []v1.VolumeMount

	for _, addon := range s.cluster.Spec.Addons {
		namespace := k3kSystemNamespace
		if addon.SecretNamespace != "" {
			namespace = addon.SecretNamespace
		}

		nn := types.NamespacedName{
			Name:      addon.SecretRef,
			Namespace: namespace,
		}

		var addons v1.Secret
		if err := s.client.Get(ctx, nn, &addons); err != nil {
			return nil, err
		}

		clusterAddons := v1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      addons.Name,
				Namespace: util.ClusterNamespace(s.cluster),
			},
			Data: make(map[string][]byte, len(addons.Data)),
		}
		for k, v := range addons.Data {
			clusterAddons.Data[k] = v
		}

		if err := s.client.Create(ctx, &clusterAddons); err != nil {
			return nil, err
		}

		name := "varlibrancherk3smanifests" + addon.SecretRef
		volume := v1.Volume{
			Name: name,
			VolumeSource: v1.VolumeSource{
				Secret: &v1.SecretVolumeSource{
					SecretName: addon.SecretRef,
				},
			},
		}
		volumes = append(volumes, volume)

		volumeMount := v1.VolumeMount{
			Name:      name,
			MountPath: "/var/lib/rancher/k3s/server/manifests/" + addon.SecretRef,
			// changes to this part of the filesystem shouldn't be done manually. The secret should be updated instead.
			ReadOnly: true,
		}
		volumeMounts = append(volumeMounts, volumeMount)
	}

	podSpec := s.podSpec(ctx, image, name, persistent)
	podSpec.Volumes = append(podSpec.Volumes, volumes...)
	podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, volumeMounts...)

	return &apps.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
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
				},
			},
			VolumeClaimTemplates: pvClaims,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"cluster": cluster.Name,
						"role":    "server",
					},
				},
				Spec: podSpec,
			},
		},
	}, nil
}
