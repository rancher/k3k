package server

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/cluster/mounts"
)

const (
	serverName     = "server"
	configName     = "server-config"
	initConfigName = "init-server-config"
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

func (s *Server) podSpec(ctx context.Context, image, name string, persistent bool, startupCmd string) corev1.PodSpec {
	log := ctrl.LoggerFrom(ctx)

	// Use the server affinity from the policy status if it exists, otherwise fall back to the spec.
	serverAffinity := s.cluster.Spec.ServerAffinity
	if s.cluster.Status.Policy != nil && s.cluster.Status.Policy.ServerAffinity != nil {
		log.V(1).Info("Using server affinity from policy", "policyName", s.cluster.Status.PolicyName, "clusterName", s.cluster.Name)
		serverAffinity = s.cluster.Status.Policy.ServerAffinity
	}

	podSpec := corev1.PodSpec{
		Affinity:          serverAffinity,
		NodeSelector:      s.cluster.Spec.NodeSelector,
		PriorityClassName: s.cluster.Spec.PriorityClass,
		Volumes: []corev1.Volume{
			{
				Name: "initconfig",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: configSecretName(s.cluster.Name, true),
						Items: []corev1.KeyToPath{
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
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: configSecretName(s.cluster.Name, false),
						Items: []corev1.KeyToPath{
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
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varrun",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibcni",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlog",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "varlibkubelet",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
		Containers: []corev1.Container{
			{
				Name:            name,
				Image:           image,
				ImagePullPolicy: corev1.PullPolicy(s.imagePullPolicy),
				Env: []corev1.EnvVar{
					{
						Name: "POD_NAME",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								FieldPath: "metadata.name",
							},
						},
					},
					{
						Name: "POD_IP",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								FieldPath: "status.podIP",
							},
						},
					},
				},
				VolumeMounts: []corev1.VolumeMount{
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
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: "varlibrancherk3s",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		)
	}

	// Adding readiness probes to statefulset
	podSpec.Containers[0].ReadinessProbe = &corev1.Probe{
		InitialDelaySeconds: 60,
		FailureThreshold:    5,
		TimeoutSeconds:      10,
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromInt(6443),
			},
		},
	}
	podSpec.Containers[0].LivenessProbe = &corev1.Probe{
		InitialDelaySeconds: 10,
		FailureThreshold:    3,
		PeriodSeconds:       3,
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{
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
		podSpec.Containers[0].SecurityContext = &corev1.SecurityContext{
			Privileged: ptr.To(true),
		}
	}

	securityContext := s.cluster.Spec.SecurityContext
	if s.cluster.Status.Policy != nil && s.cluster.Status.Policy.SecurityContext != nil {
		log.V(1).Info("Using securityContext configuration from policy", "policyName", s.cluster.Status.PolicyName, "clusterName", s.cluster.Name)
		securityContext = s.cluster.Status.Policy.SecurityContext
	}

	if securityContext != nil {
		podSpec.Containers[0].SecurityContext = securityContext
	}

	runtimeClassName := s.cluster.Spec.RuntimeClassName
	if s.cluster.Status.Policy != nil && s.cluster.Status.Policy.RuntimeClassName != nil {
		log.V(1).Info("Using runtimeClassName from policy", "policyName", s.cluster.Status.PolicyName, "clusterName", s.cluster.Name)
		runtimeClassName = s.cluster.Status.Policy.RuntimeClassName
	}

	podSpec.RuntimeClassName = runtimeClassName

	hostUsers := s.cluster.Spec.HostUsers
	if s.cluster.Status.Policy != nil && s.cluster.Status.Policy.HostUsers != nil {
		log.V(1).Info("Using hostUsers from policy", "policyName", s.cluster.Status.PolicyName, "clusterName", s.cluster.Name)
		hostUsers = s.cluster.Status.Policy.HostUsers
	}
	podSpec.HostUsers = hostUsers

	// specify resource limits if specified for the servers.
	if s.cluster.Spec.ServerLimit != nil {
		podSpec.Containers[0].Resources = corev1.ResourceRequirements{
			Limits: s.cluster.Spec.ServerLimit,
		}
	}

	podSpec.Containers[0].Env = append(podSpec.Containers[0].Env, s.cluster.Spec.ServerEnvs...)

	// add image pull secrets
	for _, imagePullSecret := range s.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: imagePullSecret})
	}

	return podSpec
}

func (s *Server) StatefulServer(ctx context.Context) (*appsv1.StatefulSet, error) {
	var (
		replicas   int32
		pvClaim    corev1.PersistentVolumeClaim
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
		volumes      []corev1.Volume
		volumeMounts []corev1.VolumeMount
	)

	if len(s.cluster.Spec.Addons) > 0 {
		addonsVols, addonsMounts, err := s.buildAddonsVolumes(ctx)
		if err != nil {
			return nil, err
		}

		volumes = append(volumes, addonsVols...)

		volumeMounts = append(volumeMounts, addonsMounts...)
	}

	if s.cluster.Spec.CustomCAs != nil && s.cluster.Spec.CustomCAs.Enabled {
		vols, mounts, err := s.buildCABundleVolumes(ctx)
		if err != nil {
			return nil, err
		}

		volumes = append(volumes, vols...)

		volumeMounts = append(volumeMounts, mounts...)
	}

	if len(s.cluster.Spec.SecretMounts) > 0 {
		vols, mounts := mounts.BuildSecretsMountsVolumes(s.cluster.Spec.SecretMounts, "server")

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

	podSpec := s.podSpec(ctx, image, name, persistent, startupCommand)
	podSpec.Volumes = append(podSpec.Volumes, volumes...)
	podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, volumeMounts...)

	ss := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.cluster.Namespace,
			Labels:    selector.MatchLabels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: headlessServiceName(s.cluster.Name),
			Selector:    &selector,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector.MatchLabels,
				},
				Spec: podSpec,
			},
		},
	}
	if s.cluster.Spec.Persistence.Type == v1beta1.DynamicPersistenceMode {
		ss.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{pvClaim}
	}

	return ss, nil
}

func (s *Server) setupDynamicPersistence() corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "varlibrancherk3s",
			Namespace: s.cluster.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: s.cluster.Spec.Persistence.StorageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": *s.cluster.Spec.Persistence.StorageRequestSize,
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
		"ETCD_DIR":      "/var/lib/rancher/k3s/server/db/etcd",
		"INIT_CONFIG":   "/opt/rancher/k3s/init/config.yaml",
		"SERVER_CONFIG": "/opt/rancher/k3s/server/config.yaml",
		"CLUSTER_MODE":  mode,
		"K3K_MODE":      string(s.cluster.Spec.Mode),
		"EXTRA_ARGS":    strings.Join(s.cluster.Spec.ServerArgs, " "),
	}); err != nil {
		return "", err
	}

	return output.String(), nil
}

func (s *Server) buildCABundleVolumes(ctx context.Context) ([]corev1.Volume, []corev1.VolumeMount, error) {
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
		volumes       []corev1.Volume
		mounts        []corev1.VolumeMount
		sortedCertIDs = sortedKeys(caCertMap)
	)

	for _, certName := range sortedCertIDs {
		var certSecret corev1.Secret

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

func (s *Server) mountCACert(volumeName, certName, secretName string, subPathMount string) (*corev1.Volume, []corev1.VolumeMount) {
	var (
		volume *corev1.Volume
		mounts []corev1.VolumeMount
	)

	// avoid re-adding secretName in case of combined secret

	volume = &corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: secretName},
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
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: fmt.Sprintf("/var/lib/rancher/k3s/server/tls%s/%s.crt", etcdPrefix, mountFile),
			SubPath:   subPathMount + ".crt",
		})
	}

	// add the mount for the key
	mounts = append(mounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: fmt.Sprintf("/var/lib/rancher/k3s/server/tls%s/%s.key", etcdPrefix, mountFile),
		SubPath:   subPathMount + ".key",
	})

	return volume, mounts
}

func (s *Server) buildAddonsVolumes(ctx context.Context) ([]corev1.Volume, []corev1.VolumeMount, error) {
	var (
		volumes []corev1.Volume
		mounts  []corev1.VolumeMount
	)

	for _, addon := range s.cluster.Spec.Addons {
		namespace := s.cluster.Namespace
		if addon.SecretNamespace != "" {
			namespace = addon.SecretNamespace
		}

		nn := types.NamespacedName{
			Name:      addon.SecretRef,
			Namespace: namespace,
		}

		var addons corev1.Secret
		if err := s.client.Get(ctx, nn, &addons); err != nil {
			return nil, nil, err
		}

		// skip creating the addon secret if it already exists and in the same namespace as the cluster
		if namespace != s.cluster.Namespace {
			clusterAddons := corev1.Secret{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Secret",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      addons.Name,
					Namespace: s.cluster.Namespace,
				},
				Data: addons.Data,
			}

			if _, err := controllerutil.CreateOrUpdate(ctx, s.client, &clusterAddons, func() error {
				return controllerutil.SetOwnerReference(s.cluster, &clusterAddons, s.client.Scheme())
			}); err != nil {
				return nil, nil, err
			}
		}

		name := "addon-" + addon.SecretRef
		volume := corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: addon.SecretRef,
				},
			},
		}
		volumes = append(volumes, volume)

		volumeMount := corev1.VolumeMount{
			Name:      name,
			MountPath: "/var/lib/rancher/k3s/server/manifests/" + addon.SecretRef,
			ReadOnly:  true,
		}
		mounts = append(mounts, volumeMount)
	}

	return volumes, mounts, nil
}

func sortedKeys(keyMap map[string]string) []string {
	keys := make([]string, 0, len(keyMap))

	for k := range keyMap {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
