package agent

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/utils/ptr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
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

	configSecret := &corev1.Secret{
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
	podSpec := v.podSpec(ctx, image, name)

	if len(v.cluster.Spec.SecretMounts) > 0 {
		vols, volMounts := mounts.BuildSecretsMountsVolumes(v.cluster.Spec.SecretMounts, "agent")

		podSpec.Volumes = append(podSpec.Volumes, vols...)

		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, volMounts...)
	}

	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.Name(),
			Namespace: v.cluster.Namespace,
			Labels:    selector.MatchLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: v.cluster.Spec.Agents,
			Selector: &selector,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector.MatchLabels,
				},
				Spec: podSpec,
			},
		},
	}

	return v.ensureObject(ctx, deployment)
}

func (v *VirtualAgent) podSpec(ctx context.Context, image, name string) corev1.PodSpec {
	log := ctrl.LoggerFrom(ctx)

	var limit corev1.ResourceList

	args := v.cluster.Spec.AgentArgs
	args = append([]string{"agent", "--config", "/opt/rancher/k3s/config.yaml"}, args...)

	// Use the agent affinity from the policy status if it exists, otherwise fall back to the spec.
	agentAffinity := v.cluster.Spec.AgentAffinity
	if v.cluster.Status.Policy != nil && v.cluster.Status.Policy.AgentAffinity != nil {
		log.V(1).Info("Using agent affinity from policy", "policyName", v.cluster.Status.PolicyName, "clusterName", v.cluster.Name)
		agentAffinity = v.cluster.Status.Policy.AgentAffinity
	}

	podSpec := corev1.PodSpec{
		Affinity:     agentAffinity,
		NodeSelector: v.cluster.Spec.NodeSelector,
		Volumes: []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: configSecretName(v.cluster.Name),
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
			{
				Name: "varlibrancherk3s",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
		Containers: []corev1.Container{
			{
				Name:            name,
				Image:           image,
				ImagePullPolicy: corev1.PullPolicy(v.ImagePullPolicy),
				SecurityContext: &corev1.SecurityContext{
					Privileged: ptr.To(true),
				},
				Args: args,
				Command: []string{
					"/bin/k3s",
				},
				Resources: corev1.ResourceRequirements{
					Limits: limit,
				},
				Env: v.cluster.Spec.AgentEnvs,
				VolumeMounts: []corev1.VolumeMount{
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
		podSpec.Containers[0].Resources = corev1.ResourceRequirements{
			Limits: v.cluster.Spec.WorkerLimit,
		}
	}

	for _, imagePullSecret := range v.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: imagePullSecret})
	}

	securityContext := v.cluster.Spec.SecurityContext
	if v.cluster.Status.Policy != nil && v.cluster.Status.Policy.SecurityContext != nil {
		log.V(1).Info("Using securityContext configuration from policy", "policyName", v.cluster.Status.PolicyName, "clusterName", v.cluster.Name)
		securityContext = v.cluster.Status.Policy.SecurityContext
	}

	if securityContext != nil {
		podSpec.Containers[0].SecurityContext = securityContext
	}

	runtimeClassName := v.cluster.Spec.RuntimeClassName
	if v.cluster.Status.Policy != nil && v.cluster.Status.Policy.RuntimeClassName != nil {
		log.V(1).Info("Using runtimeClassName from policy", "policyName", v.cluster.Status.PolicyName, "clusterName", v.cluster.Name)
		runtimeClassName = v.cluster.Status.Policy.RuntimeClassName
	}

	podSpec.RuntimeClassName = runtimeClassName

	hostUsers := v.cluster.Spec.HostUsers
	if v.cluster.Status.Policy != nil && v.cluster.Status.Policy.HostUsers != nil {
		log.V(1).Info("Using hostUsers from policy", "policyName", v.cluster.Status.PolicyName, "clusterName", v.cluster.Name)
		hostUsers = v.cluster.Status.Policy.HostUsers
	}
	podSpec.HostUsers = hostUsers

	return podSpec
}
