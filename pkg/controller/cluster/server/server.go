package server

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
)

const (
	k3kSystemNamespace = "k3k-system"
	serverName         = "server"
	configName         = "server-config"
	initConfigName     = "init-server-config"
)

// Server
type Server struct {
	cluster          *v1beta1.Cluster
	client           client.Client
	mode             string
	token            string
	image            string
	imagePullPolicy  string
	imagePullSecrets []string
}

func New(cluster *v1beta1.Cluster, client client.Client, token, image, imagePullPolicy string, imagePullSecrets []string) *Server {
	return &Server{
		cluster:          cluster,
		client:           client,
		token:            token,
		mode:             string(cluster.Spec.Mode),
		image:            image,
		imagePullPolicy:  imagePullPolicy,
		imagePullSecrets: imagePullSecrets,
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
				ImagePullPolicy: v1.PullPolicy(s.imagePullPolicy),
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

	// add image pull secrets
	for _, imagePullSecret := range s.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, v1.LocalObjectReference{Name: imagePullSecret})
	}

	return podSpec
}

func (s *Server) StatefulServer(ctx context.Context) (*apps.StatefulSet, error) {
	var (
		replicas   int32
		pvClaim    v1.PersistentVolumeClaim
		err        error
		persistent bool
	)

	image := controller.K3SImage(s.cluster, s.image)
	name := controller.SafeConcatNameWithPrefix(s.cluster.Name, serverName)

	replicas = *s.cluster.Spec.Servers

	if s.cluster.Spec.Persistence.Type == v1beta1.DynamicPersistenceMode {
		persistent = true
		pvClaim = s.setupDynamicPersistence()

		if err := controllerutil.SetControllerReference(s.cluster, &pvClaim, s.client.Scheme()); err != nil {
			return nil, err
		}
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

	if s.cluster.Spec.CustomCAs != nil && s.cluster.Spec.CustomCAs.Enabled {
		vols, mounts, err := s.loadCACertBundle(ctx)
		if err != nil {
			return nil, err
		}

		volumes = append(volumes, vols...)

		volumeMounts = append(volumeMounts, mounts...)
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
	if s.cluster.Spec.Persistence.Type == v1beta1.DynamicPersistenceMode {
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

	tmpl := StartupCommand

	mode := "single"
	if *s.cluster.Spec.Servers > 1 {
		mode = "ha"
	}

	tmplCmd, err := template.New("").Parse(tmpl)
	if err != nil {
		return "", err
	}

	if err := tmplCmd.Execute(&output, map[string]string{
		"ETCD_DIR":           "/var/lib/rancher/k3s/server/db/etcd",
		"INIT_CONFIG":        "/opt/rancher/k3s/init/config.yaml",
		"SERVER_CONFIG":      "/opt/rancher/k3s/server/config.yaml",
		"ORCHESTRATION_MODE": mode,
		"K3K_MODE":           string(s.cluster.Spec.Mode),
		"EXTRA_ARGS":         strings.Join(s.cluster.Spec.ServerArgs, " "),
	}); err != nil {
		return "", err
	}

	return output.String(), nil
}

func (s *Server) loadCACertBundle(ctx context.Context) ([]v1.Volume, []v1.VolumeMount, error) {
	if s.cluster.Spec.CustomCAs == nil {
		return nil, nil, fmt.Errorf("customCAs not found")
	}

	customCerts := s.cluster.Spec.CustomCAs.Sources
	caCertMap := map[string]string{
		"server-ca":         customCerts.ServerCA.SecretName,
		"client-ca":         customCerts.ClientCA.SecretName,
		"request-header-ca": customCerts.RequestHeaderCA.SecretName,
		"etcd-peer-ca":      customCerts.ETCDPeerCA.SecretName,
		"etcd-server-ca":    customCerts.ETCDServerCA.SecretName,
		"service":           customCerts.ServiceAccountToken.SecretName,
	}

	var (
		volumes       []v1.Volume
		mounts        []v1.VolumeMount
		sortedCertIDs = sortedKeys(caCertMap)
	)

	for _, certName := range sortedCertIDs {
		var certSecret v1.Secret

		secretName := string(caCertMap[certName])
		key := types.NamespacedName{Name: secretName, Namespace: s.cluster.Namespace}

		if err := s.client.Get(ctx, key, &certSecret); err != nil {
			return nil, nil, err
		}

		cert := certSecret.Data["tls.crt"]
		keyData := certSecret.Data["tls.key"]

		// Service account token secret is an exception (may not contain crt/key).
		if certName != "service" && (len(cert) == 0 || len(keyData) == 0) {
			return nil, nil, fmt.Errorf("cert or key is not found in secret %s", certName)
		}

		volumeName := certName + "-vol"

		vol, certMounts := s.mountCACert(volumeName, certName, secretName, "tls")
		volumes = append(volumes, *vol)
		mounts = append(mounts, certMounts...)
	}

	return volumes, mounts, nil
}

func (s *Server) mountCACert(volumeName, certName, secretName string, subPathMount string) (*v1.Volume, []v1.VolumeMount) {
	var (
		volume *v1.Volume
		mounts []v1.VolumeMount
	)

	// avoid re-adding secretName in case of combined secret

	volume = &v1.Volume{
		Name: volumeName,
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{SecretName: secretName},
		},
	}

	etcdPrefix := ""

	mountFile := certName

	if strings.HasPrefix(certName, "etcd-") {
		etcdPrefix = "/etcd"
		mountFile = strings.TrimPrefix(certName, "etcd-")
	}

	// add the mount for the cert except for the service account token
	if certName != "service" {
		mounts = append(mounts, v1.VolumeMount{
			Name:      volumeName,
			MountPath: fmt.Sprintf("/var/lib/rancher/k3s/server/tls%s/%s.crt", etcdPrefix, mountFile),
			SubPath:   subPathMount + ".crt",
		})
	}

	// add the mount for the key
	mounts = append(mounts, v1.VolumeMount{
		Name:      volumeName,
		MountPath: fmt.Sprintf("/var/lib/rancher/k3s/server/tls%s/%s.key", etcdPrefix, mountFile),
		SubPath:   subPathMount + ".key",
	})

	return volume, mounts
}

func sortedKeys(keyMap map[string]string) []string {
	keys := make([]string, 0, len(keyMap))

	for k := range keyMap {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
