package agent

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/util/intstr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
)

const (
	SharedNodeAgentName = "kubelet"
	SharedNodeMode      = "shared"
)

type SharedAgent struct {
	*Config
	serviceIP        string
	image            string
	imagePullPolicy  string
	imageRegistry    string
	token            string
	kubeletPort      int
	imagePullSecrets []string
}

func NewSharedAgent(config *Config, serviceIP, image, imagePullPolicy, token string, kubeletPort int, imagePullSecrets []string) *SharedAgent {
	return &SharedAgent{
		Config:           config,
		serviceIP:        serviceIP,
		image:            image,
		imagePullPolicy:  imagePullPolicy,
		token:            token,
		kubeletPort:      kubeletPort,
		imagePullSecrets: imagePullSecrets,
	}
}

func (s *SharedAgent) Name() string {
	return controller.SafeConcatNameWithPrefix(s.cluster.Name, SharedNodeAgentName)
}

func (s *SharedAgent) EnsureResources(ctx context.Context) error {
	if err := errors.Join(
		s.config(ctx),
		s.serviceAccount(ctx),
		s.role(ctx),
		s.roleBinding(ctx),
		s.service(ctx),
		s.daemonset(ctx),
		s.dnsService(ctx),
	); err != nil {
		return fmt.Errorf("failed to ensure some resources: %w", err)
	}

	return nil
}

func (s *SharedAgent) ensureObject(ctx context.Context, obj ctrlruntimeclient.Object) error {
	return ensureObject(ctx, s.Config, obj)
}

func (s *SharedAgent) config(ctx context.Context) error {
	config := sharedAgentData(s.cluster, s.Name(), s.token, s.serviceIP, s.kubeletPort)

	configSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      configSecretName(s.cluster.Name),
			Namespace: s.cluster.Namespace,
		},
		Data: map[string][]byte{
			"config.yaml": []byte(config),
		},
	}

	return s.ensureObject(ctx, configSecret)
}

func sharedAgentData(cluster *v1beta1.Cluster, serviceName, token, ip string, kubeletPort int) string {
	version := cluster.Spec.Version
	if cluster.Spec.Version == "" {
		version = cluster.Status.HostVersion
	}

	return fmt.Sprintf(`clusterName: %s
clusterNamespace: %s
serverIP: %s
serviceName: %s
token: %v
mirrorHostNodes: %t
version: %s
kubeletPort: %d`,
		cluster.Name, cluster.Namespace, ip, serviceName, token, cluster.Spec.MirrorHostNodes, version, kubeletPort)
}

func (s *SharedAgent) daemonset(ctx context.Context) error {
	labels := map[string]string{
		"cluster": s.cluster.Name,
		"type":    "agent",
		"mode":    "shared",
	}

	deploy := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: s.podSpec(ctx),
			},
		},
	}

	return s.ensureObject(ctx, deploy)
}

func (s *SharedAgent) podSpec(ctx context.Context) corev1.PodSpec {
	log := ctrl.LoggerFrom(ctx)

	hostNetwork := false
	dnsPolicy := corev1.DNSClusterFirst

	if s.cluster.Spec.MirrorHostNodes {
		hostNetwork = true
		dnsPolicy = corev1.DNSClusterFirstWithHostNet
	}

	image := s.image

	if s.imageRegistry != "" {
		image = s.imageRegistry + "/" + s.image
	}

	// Use the agent affinity from the policy status if it exists, otherwise fall back to the spec.
	agentAffinity := s.cluster.Spec.AgentAffinity
	if s.cluster.Status.Policy != nil && s.cluster.Status.Policy.AgentAffinity != nil {
		log.V(1).Info("Using agent affinity from policy", "policyName", s.cluster.Status.PolicyName, "clusterName", s.cluster.Name)
		agentAffinity = s.cluster.Status.Policy.AgentAffinity
	}

	podSpec := corev1.PodSpec{
		Affinity:           agentAffinity,
		HostNetwork:        hostNetwork,
		DNSPolicy:          dnsPolicy,
		ServiceAccountName: s.Name(),
		NodeSelector:       s.cluster.Spec.NodeSelector,
		Volumes: []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: configSecretName(s.cluster.Name),
						Items: []corev1.KeyToPath{
							{
								Key:  "config.yaml",
								Path: "config.yaml",
							},
						},
					},
				},
			},
		},
		Containers: []corev1.Container{
			{
				Name:            s.Name(),
				Image:           image,
				ImagePullPolicy: corev1.PullPolicy(s.imagePullPolicy),
				Env: append([]corev1.EnvVar{
					{
						Name: "AGENT_HOSTNAME",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "spec.nodeName",
							},
						},
					},
					{
						Name: "POD_IP",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "status.podIP",
							},
						},
					},
				}, s.cluster.Spec.AgentEnvs...),
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "config",
						MountPath: "/opt/rancher/k3k/",
						ReadOnly:  false,
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "kubelet-port",
						Protocol:      corev1.ProtocolTCP,
						ContainerPort: int32(s.kubeletPort),
					},
				},
			},
		},
	}
	for _, imagePullSecret := range s.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: imagePullSecret})
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

	// specify resource limits if specified for the agents.
	if s.cluster.Spec.WorkerLimit != nil {
		podSpec.Containers[0].Resources = corev1.ResourceRequirements{
			Limits: s.cluster.Spec.WorkerLimit,
		}
	}

	// specifying WorkerResources will take precedence over WorkerLimits
	if s.cluster.Spec.WorkerResources != nil {
		// removing container previous limit
		podSpec.Containers[0].Resources = corev1.ResourceRequirements{}
		podSpec.Resources = s.cluster.Spec.WorkerResources
	}

	return podSpec
}

func (s *SharedAgent) service(ctx context.Context) error {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"cluster": s.cluster.Name,
				"type":    "agent",
				"mode":    "shared",
			},
			Ports: []corev1.ServicePort{
				{
					Name:     "k3s-kubelet-port",
					Protocol: corev1.ProtocolTCP,
					Port:     int32(s.kubeletPort),
				},
			},
		},
	}

	return s.ensureObject(ctx, svc)
}

func (s *SharedAgent) dnsService(ctx context.Context) error {
	dnsServiceName := controller.SafeConcatNameWithPrefix(s.cluster.Name, "kube-dns")

	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      dnsServiceName,
			Namespace: s.cluster.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				translate.ClusterNameLabel: s.cluster.Name,
				"k8s-app":                  "kube-dns",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "dns",
					Protocol:   corev1.ProtocolUDP,
					Port:       53,
					TargetPort: intstr.FromInt32(53),
				},
				{
					Name:       "dns-tcp",
					Protocol:   corev1.ProtocolTCP,
					Port:       53,
					TargetPort: intstr.FromInt32(53),
				},
				{
					Name:       "metrics",
					Protocol:   corev1.ProtocolTCP,
					Port:       9153,
					TargetPort: intstr.FromInt32(9153),
				},
			},
		},
	}

	return s.ensureObject(ctx, svc)
}

func (s *SharedAgent) serviceAccount(ctx context.Context) error {
	svcAccount := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
		},
	}

	return s.ensureObject(ctx, svcAccount)
}

func (s *SharedAgent) role(ctx context.Context) error {
	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumeclaims", "pods", "pods/log", "pods/attach", "pods/exec", "pods/ephemeralcontainers", "secrets", "configmaps", "services"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"resourcequotas"},
				Verbs:     []string{"get", "watch", "list"},
			},
			{
				APIGroups: []string{"networking.k8s.io"},
				Resources: []string{"ingresses"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"k3k.io"},
				Resources: []string{"clusters"},
				Verbs:     []string{"get", "watch", "list"},
			},
			{
				APIGroups: []string{"coordination.k8s.io"},
				Resources: []string{"leases"},
				Verbs:     []string{"*"},
			},
		},
	}

	return s.ensureObject(ctx, role)
}

func (s *SharedAgent) roleBinding(ctx context.Context) error {
	roleBinding := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     s.Name(),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      s.Name(),
				Namespace: s.cluster.Namespace,
			},
		},
	}

	return s.ensureObject(ctx, roleBinding)
}
