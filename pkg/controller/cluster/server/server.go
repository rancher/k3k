package server

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	k3kSystemNamespace = "k3k-system"
	serverName         = "server"
	configName         = "server-config"
	initConfigName     = "init-server-config"
)

// Server
type Server struct {
	cluster            *v1alpha1.Cluster
	client             client.Client
	mode               string
	token              string
	k3SImage           string
	k3SImagePullPolicy string
}

func New(cluster *v1alpha1.Cluster, client client.Client, token string, k3SImage string, k3SImagePullPolicy string) *Server {
	return &Server{
		cluster:            cluster,
		client:             client,
		token:              token,
		mode:               string(cluster.Spec.Mode),
		k3SImage:           k3SImage,
		k3SImagePullPolicy: k3SImagePullPolicy,
	}
}

func (s *Server) podSpec(image, name string, persistent bool, startupCmd string) v1.PodSpec {
	podSpec := v1.PodSpec{
		NodeSelector:      s.cluster.Spec.NodeSelector,
		PriorityClassName: s.cluster.Spec.PriorityClass,
		Volumes: []v1.Volume{
			{
				Name: "initconfig",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: configSecretName(s.cluster.Name, true),
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
						SecretName: configSecretName(s.cluster.Name, false),
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
		},
		Containers: []v1.Container{
			{
				Name:            name,
				Image:           image,
				ImagePullPolicy: v1.PullPolicy(s.k3SImagePullPolicy),
				Env: []v1.EnvVar{
					{
						Name: "POD_NAME",
						ValueFrom: &v1.EnvVarSource{
							FieldRef: &v1.ObjectFieldSelector{
								FieldPath: "metadata.name",
							},
						},
					},
					{
						Name: "POD_IP",
						ValueFrom: &v1.EnvVarSource{
							FieldRef: &v1.ObjectFieldSelector{
								FieldPath: "status.podIP",
							},
						},
					},
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

	cmd := []string{
		"/bin/sh",
		"-c",
		startupCmd,
	}

	podSpec.Containers[0].Command = cmd
	if !persistent {
		podSpec.Volumes = append(podSpec.Volumes, v1.Volume{
			Name: "varlibrancherk3s",
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{},
			},
		},
		)
	}

	// Adding readiness probes to statefulset
	podSpec.Containers[0].ReadinessProbe = &v1.Probe{
		InitialDelaySeconds: 60,
		FailureThreshold:    5,
		TimeoutSeconds:      10,
		ProbeHandler: v1.ProbeHandler{
			TCPSocket: &v1.TCPSocketAction{
				Port: intstr.FromInt(6443),
			},
		},
	}
	podSpec.Containers[0].LivenessProbe = &v1.Probe{
		InitialDelaySeconds: 10,
		FailureThreshold:    3,
		PeriodSeconds:       3,
		ProbeHandler: v1.ProbeHandler{
			Exec: &v1.ExecAction{
				Command: []string{
					"sh",
					"-c",
					`grep -q "rejoin the cluster" /var/log/k3s.log && exit 1 || exit 0`,
				},
			},
		},
	}
	// start the pod unprivileged in shared mode
	if s.mode == agent.VirtualNodeMode {
		podSpec.Containers[0].SecurityContext = &v1.SecurityContext{
			Privileged: ptr.To(true),
		}
	}

	// specify resource limits if specified for the servers.
	if s.cluster.Spec.ServerLimit != nil {
		podSpec.Containers[0].Resources = v1.ResourceRequirements{
			Limits: s.cluster.Spec.ServerLimit,
		}
	}

	podSpec.Containers[0].Env = append(podSpec.Containers[0].Env, s.cluster.Spec.ServerEnvs...)

	return podSpec
}

func (s *Server) StatefulServer(ctx context.Context) (*apps.StatefulSet, error) {
	var (
		replicas   int32
		pvClaim    v1.PersistentVolumeClaim
		err        error
		persistent bool
	)

	image := controller.K3SImage(s.cluster, s.k3SImage)
	name := controller.SafeConcatNameWithPrefix(s.cluster.Name, serverName)

	replicas = *s.cluster.Spec.Servers

	if s.cluster.Spec.Persistence.Type == v1alpha1.DynamicPersistenceMode {
		persistent = true
		pvClaim = s.setupDynamicPersistence()
	}

	var (
		volumes      []v1.Volume
		volumeMounts []v1.VolumeMount
	)

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
				Namespace: s.cluster.Namespace,
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

	if s.cluster.Spec.CustomCertificates.Enabled {
		var certSecret v1.Secret
		key := types.NamespacedName{
			Name:      controller.SafeConcatNameWithPrefix(s.cluster.Name, "custom", "certs"),
			Namespace: s.cluster.Namespace,
		}
		if err := s.client.Get(ctx, key, &certSecret); err != nil {
			return nil, err
		}
		// adding volume and volume mounts for certs
		name := "cert-volume"
		secretName := s.cluster.Spec.CustomCertificates.SecretName
		if secretName == "" {
			secretName = key.Name
		}
		certVolume := v1.Volume{
			Name: name,
			VolumeSource: v1.VolumeSource{
				Secret: &v1.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		}
		volumes = append(volumes, certVolume)
		if len(certSecret.Data) <= 0 {
			return nil, fmt.Errorf("No certificate files found in the secret.")
		}
		// adding volume mounts
		var keys []string
		for key := range certSecret.Data {
			keys = append(keys, key)
		}
		// Sort the keys before iterating over them to ensure predictable order
		// otherwise the volume mount order with each reconcile causing the pod to restart.
		sort.Strings(keys)
		for _, certName := range keys {
			var etcdPrefix string
			certFile := certName
			if strings.Contains(certName, "etcd-") {
				etcdPrefix = "/etcd"
				certFile = certName[5:] // "etcd-"
			}
			certVolumeMount := v1.VolumeMount{
				Name:      name,
				MountPath: "/var/lib/rancher/k3s/server/tls" + etcdPrefix + "/" + certFile,
				SubPath:   certName,
			}
			volumeMounts = append(volumeMounts, certVolumeMount)
		}

	}

	selector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"cluster": s.cluster.Name,
			"role":    "server",
		},
	}

	startupCommand, err := s.setupStartCommand()
	if err != nil {
		return nil, err
	}

	podSpec := s.podSpec(image, name, persistent, startupCommand)
	podSpec.Volumes = append(podSpec.Volumes, volumes...)
	podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, volumeMounts...)

	ss := &apps.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.cluster.Namespace,
			Labels:    selector.MatchLabels,
		},
		Spec: apps.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: headlessServiceName(s.cluster.Name),
			Selector:    &selector,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector.MatchLabels,
				},
				Spec: podSpec,
			},
		},
	}
	if s.cluster.Spec.Persistence.Type == v1alpha1.DynamicPersistenceMode {
		ss.Spec.VolumeClaimTemplates = []v1.PersistentVolumeClaim{pvClaim}
	}

	return ss, nil
}

func (s *Server) setupDynamicPersistence() v1.PersistentVolumeClaim {
	return v1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "varlibrancherk3s",
			Namespace: s.cluster.Namespace,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			StorageClassName: s.cluster.Spec.Persistence.StorageClassName,
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{
					"storage": resource.MustParse(s.cluster.Spec.Persistence.StorageRequestSize),
				},
			},
		},
	}
}

func (s *Server) setupStartCommand() (string, error) {
	var output bytes.Buffer

	tmpl := singleServerTemplate
	if *s.cluster.Spec.Servers > 1 {
		tmpl = HAServerTemplate
	}

	tmplCmd, err := template.New("").Parse(tmpl)
	if err != nil {
		return "", err
	}

	if err := tmplCmd.Execute(&output, map[string]string{
		"ETCD_DIR":      "/var/lib/rancher/k3s/server/db/etcd",
		"INIT_CONFIG":   "/opt/rancher/k3s/init/config.yaml",
		"SERVER_CONFIG": "/opt/rancher/k3s/server/config.yaml",
		"EXTRA_ARGS":    strings.Join(s.cluster.Spec.ServerArgs, " "),
	}); err != nil {
		return "", err
	}

	return output.String(), nil
}
