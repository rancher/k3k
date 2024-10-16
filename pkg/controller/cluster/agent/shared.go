package agent

import (
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	virtualKubeletImage      = "rancher/k3k:k3k-kubelet"
	virtualKubeletConfigPath = "/opt/rancher/k3k/config.yaml"
)

type SharedAgent struct {
	cluster   *v1alpha1.Cluster
	serviceIP string
}

func NewSharedAgent(cluster *v1alpha1.Cluster, serviceIP string) Agent {
	return &SharedAgent{
		cluster:   cluster,
		serviceIP: serviceIP,
	}
}

func (s *SharedAgent) Config() (ctrlruntimeclient.Object, error) {
	config := sharedAgentData(s.cluster)

	return &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.AgentConfigName(s.cluster),
			Namespace: util.ClusterNamespace(s.cluster),
		},
		Data: map[string][]byte{
			"config.yaml": []byte(config),
		},
	}, nil
}

func sharedAgentData(cluster *v1alpha1.Cluster) string {
	return fmt.Sprintf(`clusterName: %s
clusterNamespace: %s
nodeName: %s
token: %s`, cluster.Name, cluster.Namespace, cluster.Name+"-"+"k3k-kubelet", cluster.Spec.Token)
}

func (s *SharedAgent) Resources() ([]ctrlruntimeclient.Object, error) {
	var objs []ctrlruntimeclient.Object
	objs = append(objs, s.serviceAccount(), s.role(), s.roleBinding(), s.deployment())
	return objs, nil
}

func (s *SharedAgent) deployment() *apps.Deployment {
	selector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"cluster": s.cluster.Name,
			"type":    "agent",
			"mode":    "shared",
		},
	}
	name := s.cluster.Name + "-" + "k3k-kubelet"
	return &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: util.ClusterNamespace(s.cluster),
			Labels:    selector.MatchLabels,
		},
		Spec: apps.DeploymentSpec{
			Selector: &selector,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector.MatchLabels,
				},
				Spec: s.podSpec(virtualKubeletImage, name, &selector),
			},
		},
	}
}

func (s *SharedAgent) podSpec(image, name string, affinitySelector *metav1.LabelSelector) v1.PodSpec {
	args := []string{"--config", virtualKubeletConfigPath}
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
		ServiceAccountName: s.cluster.Name + "-" + "k3k-kubelet",
		Volumes: []v1.Volume{
			{
				Name: "config",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: util.AgentConfigName(s.cluster),
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
				Env: []v1.EnvVar{
					{
						Name: "AGENT_POD_IP",
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
						MountPath: "/opt/rancher/k3k/",
						ReadOnly:  false,
					},
				},
			},
		}}
}

func (s *SharedAgent) serviceAccount() *v1.ServiceAccount {
	return &v1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.cluster.Name + "-" + "k3k-kubelet",
			Namespace: util.ClusterNamespace(s.cluster),
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
			Name:      s.cluster.Name + "-" + "k3k-kubelet",
			Namespace: util.ClusterNamespace(s.cluster),
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"*"},
				APIGroups: []string{""},
				Resources: []string{"pods"},
			},
			{
				Verbs:     []string{"get", "watch", "list"},
				APIGroups: []string{""},
				Resources: []string{"secrets", "services"},
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
			Name:      s.cluster.Name + "-" + "k3k-kubelet",
			Namespace: util.ClusterNamespace(s.cluster),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     s.cluster.Name + "-" + "k3k-kubelet",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      s.cluster.Name + "-" + "k3k-kubelet",
				Namespace: util.ClusterNamespace(s.cluster),
			},
		},
	}
}
