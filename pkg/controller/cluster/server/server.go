package server

import (
	"context"
	"strconv"

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

func (s *Server) Deploy(ctx context.Context, init bool) (*apps.Deployment, error) {
	var replicas int32
	image := util.K3SImage(s.cluster)

	name := serverName + "server"
	if init {
		name = serverName + "init-server"
	}

	replicas = *s.cluster.Spec.Servers - 1
	if init {
		replicas = 1
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

	podSpec := s.podSpec(ctx, image, name, false, init)

	podSpec.Volumes = append(podSpec.Volumes, volumes...)
	podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, volumeMounts...)

	return &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.cluster.Name + "-" + name,
			Namespace: util.ClusterNamespace(s.cluster),
		},
		Spec: apps.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"cluster": s.cluster.Name,
					"role":    "server",
					"init":    strconv.FormatBool(init),
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"cluster": s.cluster.Name,
						"role":    "server",
						"init":    strconv.FormatBool(init),
					},
				},
				Spec: podSpec,
			},
		},
	}, nil
}

func (s *Server) podSpec(ctx context.Context, image, name string, statefulSet, init bool) v1.PodSpec {
	args := append([]string{"server", "--config", "/opt/rancher/k3s/config.yaml"}, s.cluster.Spec.ServerArgs...)

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

	if !init {
		podSpec.InitContainers = []v1.Container{
			{
				Name:  initContainerName,
				Image: initContainerImage,
				Command: []string{
					"sh",
					"-c",
					"until curl -qk https://k3k-server-service.$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace).svc.cluster.local:6443/v1-k3s/readyz; do echo waiting for init server to be up; sleep 2; done",
				},
			},
		}
	}
	return podSpec
}

func (s *Server) StatefulServer(ctx context.Context, cluster *v1alpha1.Cluster, init bool) *apps.StatefulSet {
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
				Spec: s.podSpec(ctx, image, name, true, init),
			},
		},
	}
}
