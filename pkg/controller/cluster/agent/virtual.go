package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v4"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/mounts"
)

const (
	// VirtualNodeMode is the agent mode where a cluster runs its own k3s agents.
	VirtualNodeMode      = "virtual"
	virtualNodeAgentName = "agent"
)

const deprecationWarning = "Using Deployments for virtual mode agents is deprecated and will be replaced with StatefulSets"

// VirtualAgent runs a virtual cluster in virtual mode, where its workloads run on k3s
// agents dedicated to that cluster.
type VirtualAgent struct {
	*Config
	serviceIP        string
	token            string
	Image            string
	ImagePullPolicy  string
	ImageRegistry    string
	imagePullSecrets []string
}

type virtualAgentConfig struct {
	Server     string `yaml:"server"`
	Token      string `yaml:"token"`
	WithNodeID bool   `yaml:"with-node-id"`
}

// NewVirtualAgent returns a VirtualAgent for the cluster in config.
func NewVirtualAgent(config *Config, serviceIP, token, image, imagePullPolicy string, imagePullSecrets []string) *VirtualAgent {
	return &VirtualAgent{
		Config:           config,
		serviceIP:        serviceIP,
		token:            token,
		Image:            image,
		ImagePullPolicy:  imagePullPolicy,
		imagePullSecrets: imagePullSecrets,
	}
}

// Name returns the name shared by the agent's resources.
func (v *VirtualAgent) Name() string {
	return controller.SafeConcatNameWithPrefix(v.cluster.Name, virtualNodeAgentName)
}

// EnsureResources creates or updates every resource a virtual mode agent needs, and
// reports all the failures together.
func (v *VirtualAgent) EnsureResources(ctx context.Context) error {
	// check if deployment is in use then keep it until its fully removed
	var (
		agentsDeployment  v1.Deployment
		deploymentNotUsed bool
	)

	if err := v.client.Get(ctx, types.NamespacedName{Name: v.Name(), Namespace: v.cluster.Namespace}, &agentsDeployment); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		deploymentNotUsed = true
	}

	var allErrs error

	if deploymentNotUsed {
		allErrs = errors.Join(
			v.config(ctx, false),
			v.headlessService(ctx),
			v.statefulset(ctx),
		)
	} else {
		allErrs = errors.Join(
			v.config(ctx, true),
			v.deployment(ctx),
		)
	}

	if allErrs != nil {
		return fmt.Errorf("failed to ensure some resources: %w", allErrs)
	}

	return nil
}

func (v *VirtualAgent) ensureObject(ctx context.Context, obj ctrlruntimeclient.Object) error {
	return ensureObject(ctx, v.Config, obj)
}

func (v *VirtualAgent) config(ctx context.Context, withNodeID bool) error {
	config, err := virtualAgentData(v.serviceIP, v.token, withNodeID)
	if err != nil {
		return err
	}

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
			"config.yaml": config,
		},
	}

	return v.ensureObject(ctx, configSecret)
}

func virtualAgentData(serviceIP, token string, withNodeId bool) ([]byte, error) {
	agentConfig := virtualAgentConfig{
		Server:     "https://" + serviceIP,
		Token:      token,
		WithNodeID: withNodeId,
	}

	return yaml.Marshal(agentConfig)
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

func (v *VirtualAgent) headlessService(ctx context.Context) error {
	headlessService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.Name(),
			Namespace: v.cluster.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: corev1.ClusterIPNone,
			Selector: map[string]string{
				"cluster": v.cluster.Name,
				"type":    "agent",
				"mode":    "virtual",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "k3s-kubelet-port",
					Protocol:   corev1.ProtocolTCP,
					Port:       10250,
					TargetPort: intstr.FromInt(10250),
				},
			},
		},
	}

	return v.ensureObject(ctx, headlessService)
}

func (v *VirtualAgent) statefulset(ctx context.Context) error {
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

	ss := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.Name(),
			Namespace: v.cluster.Namespace,
			Labels:    selector.MatchLabels,
		},
		Spec: appsv1.StatefulSetSpec{
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

	return v.ensureObject(ctx, ss)
}

func (v *VirtualAgent) podSpec(ctx context.Context, image, name string) corev1.PodSpec {
	log := ctrl.LoggerFrom(ctx)

	var limit corev1.ResourceList

	args := v.cluster.Spec.AgentArgs
	args = append([]string{"agent", "--config", "/opt/rancher/k3s/config.yaml"}, args...)

	if v.ImageRegistry != "" {
		image = v.ImageRegistry + "/" + image
	}

	// Use the agent affinity from the policy status if it exists, otherwise fall back to the spec.
	agentAffinity := v.cluster.Spec.AgentAffinity
	if v.cluster.Status.Policy != nil && v.cluster.Status.Policy.AgentAffinity != nil {
		log.V(1).Info("Using agent affinity from policy", "policyName", v.cluster.Status.PolicyName, "clusterName", v.cluster.Name)
		agentAffinity = v.cluster.Status.Policy.AgentAffinity
	}

	// Use the node selector from the policy status if it exists, otherwise fall back to the spec.
	nodeSelector := v.cluster.Spec.NodeSelector
	if v.cluster.Status.Policy != nil && len(v.cluster.Status.Policy.NodeSelector) > 0 {
		log.V(1).Info("Using node selector from policy", "policyName", v.cluster.Status.PolicyName, "clusterName", v.cluster.Name)
		nodeSelector = v.cluster.Status.Policy.NodeSelector
	}

	podSpec := corev1.PodSpec{
		Affinity:     agentAffinity,
		NodeSelector: nodeSelector,
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
					Privileged: new(true),
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

	// specifying WorkerResources will take precedence over WorkerLimits
	if v.cluster.Spec.WorkerResources != nil {
		// removing container previous limit
		podSpec.Containers[0].Resources = corev1.ResourceRequirements{}
		podSpec.Resources = v.cluster.Spec.WorkerResources
	}

	for _, imagePullSecret := range v.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: imagePullSecret})
	}

	// pod security context
	podSecurityContext := v.cluster.Spec.PodSecurityContext
	if v.cluster.Status.Policy != nil && v.cluster.Status.Policy.PodSecurityContext != nil {
		log.V(1).Info("Using container pod securityContext configuration from policy", "policyName", v.cluster.Status.PolicyName, "clusterName", v.cluster.Name)
		podSecurityContext = v.cluster.Status.Policy.PodSecurityContext
	}

	podSpec.SecurityContext = podSecurityContext

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

	if podSpec.RuntimeClassName != nil && strings.HasPrefix(*podSpec.RuntimeClassName, "kata") {
		mounts.AddKmsgMount(&podSpec)

		mounts.FilterEmptyDirVolumes(&podSpec)
	}

	return podSpec
}
