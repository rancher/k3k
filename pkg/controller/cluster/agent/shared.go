package agent

import (
	"fmt"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	sharedKubeletConfigPath = "/opt/rancher/k3k/config.yaml"
	sharedNodeAgentName     = "kubelet"
	SharedNodeMode          = "shared"
)

type SharedAgent struct {
	cluster          *v1alpha1.Cluster
	serviceIP        string
	sharedAgentImage string
	token            string
}

func NewSharedAgent(cluster *v1alpha1.Cluster, serviceIP, sharedAgentImage, token string) Agent {
	return &SharedAgent{
		cluster:          cluster,
		serviceIP:        serviceIP,
		sharedAgentImage: sharedAgentImage,
		token:            token,
	}
}

func (s *SharedAgent) Config() (ctrlruntimeclient.Object, error) {
	config := sharedAgentData(s.cluster, s.token, s.Name(), s.serviceIP)

	return &v1.Secret{
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
	}, nil
}

func sharedAgentData(cluster *v1alpha1.Cluster, token, nodeName, ip string) string {
	return fmt.Sprintf(`clusterName: %s
clusterNamespace: %s
nodeName: %s
agentHostname: %s
serverIP: %s
token: %s`,
		cluster.Name, cluster.Namespace, nodeName, nodeName, ip, token)
}

func (s *SharedAgent) Resources() []ctrlruntimeclient.Object {
	return []ctrlruntimeclient.Object{s.serviceAccount(), s.role(), s.roleBinding(), s.service(), s.deployment(), s.dnsService()}
}

func (s *SharedAgent) deployment() *apps.Deployment {
	selector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"cluster": s.cluster.Name,
			"type":    "agent",
			"mode":    "shared",
		},
	}
	name := s.Name()
	return &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.cluster.Namespace,
			Labels:    selector.MatchLabels,
		},
		Spec: apps.DeploymentSpec{
			Selector: &selector,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector.MatchLabels,
				},
				Spec: s.podSpec(s.sharedAgentImage, name, &selector),
			},
		},
	}
}

func (s *SharedAgent) podSpec(image, name string, affinitySelector *metav1.LabelSelector) v1.PodSpec {
	args := []string{"--config", sharedKubeletConfigPath}
	var limit v1.ResourceList
	return v1.PodSpec{
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
		ServiceAccountName: s.Name(),
		Volumes: []v1.Volume{
			{
				Name: "config",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: configSecretName(s.cluster.Name),
						Items: []v1.KeyToPath{
							{
								Key:  "config.yaml",
								Path: "config.yaml",
							},
						},
					},
				},
			},
		},
		Containers: []v1.Container{
			{
				Name:            name,
				Image:           image,
				ImagePullPolicy: v1.PullAlways,
				Resources: v1.ResourceRequirements{
					Limits: limit,
				},
				Args: args,
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      "config",
						MountPath: "/opt/rancher/k3k/",
						ReadOnly:  false,
					},
				},
			},
		}}
}

func (s *SharedAgent) service() *v1.Service {
	return &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
		},
		Spec: v1.ServiceSpec{
			Type: v1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"cluster": s.cluster.Name,
				"type":    "agent",
				"mode":    "shared",
			},
			Ports: []v1.ServicePort{
				{
					Name:     "k3s-kubelet-port",
					Protocol: v1.ProtocolTCP,
					Port:     10250,
				},
			},
		},
	}
}

func (s *SharedAgent) dnsService() *v1.Service {
	return &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.DNSName(),
			Namespace: s.cluster.Namespace,
		},
		Spec: v1.ServiceSpec{
			Type: v1.ServiceTypeClusterIP,
			Selector: map[string]string{
				translate.ClusterNameLabel: s.cluster.Name,
				"k8s-app":                  "kube-dns",
			},
			Ports: []v1.ServicePort{
				{
					Name:       "dns",
					Protocol:   v1.ProtocolUDP,
					Port:       53,
					TargetPort: intstr.FromInt32(53),
				},
				{
					Name:       "dns-tcp",
					Protocol:   v1.ProtocolTCP,
					Port:       53,
					TargetPort: intstr.FromInt32(53),
				},
				{
					Name:       "metrics",
					Protocol:   v1.ProtocolTCP,
					Port:       9153,
					TargetPort: intstr.FromInt32(9153),
				},
			},
		},
	}
}

func (s *SharedAgent) serviceAccount() *v1.ServiceAccount {
	return &v1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
		},
	}
}

func (s *SharedAgent) role() *rbacv1.Role {
	return &rbacv1.Role{
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
				Verbs:     []string{"*"},
				APIGroups: []string{""},
				Resources: []string{"pods", "pods/log", "pods/exec", "secrets", "configmaps", "services"},
			},
			{
				Verbs:     []string{"get", "watch", "list"},
				APIGroups: []string{"k3k.io"},
				Resources: []string{"clusters"},
			},
		},
	}
}

func (s *SharedAgent) roleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
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
}

func (s *SharedAgent) Name() string {
	return controller.SafeConcatNameWithPrefix(s.cluster.Name, sharedNodeAgentName)
}

func (s *SharedAgent) DNSName() string {
	return controller.SafeConcatNameWithPrefix(s.cluster.Name, "kube-dns")
}
