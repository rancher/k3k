package agent

import (
	"crypto"
	"crypto/x509"
	"fmt"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	sharedKubeletConfigPath = "/opt/rancher/k3k/config.yaml"
	SharedNodeAgentName     = "kubelet"
	SharedNodeMode          = "shared"
)

type SharedAgent struct {
	cluster                    *v1alpha1.Cluster
	serviceIP                  string
	sharedAgentImage           string
	sharedAgentImagePullPolicy string
	token                      string
}

func NewSharedAgent(cluster *v1alpha1.Cluster, serviceIP, sharedAgentImage, sharedAgentImagePullPolicy, token string) Agent {
	return &SharedAgent{
		cluster:                    cluster,
		serviceIP:                  serviceIP,
		sharedAgentImage:           sharedAgentImage,
		sharedAgentImagePullPolicy: sharedAgentImagePullPolicy,
		token:                      token,
	}
}

func (s *SharedAgent) Config() ctrlruntimeclient.Object {
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
	}
}

func sharedAgentData(cluster *v1alpha1.Cluster, token, nodeName, ip string) string {
	version := cluster.Spec.Version
	if cluster.Spec.Version == "" {
		version = cluster.Status.HostVersion
	}
	return fmt.Sprintf(`clusterName: %s
clusterNamespace: %s
nodeName: %s
agentHostname: %s
serverIP: %s
token: %s
version: %s`,
		cluster.Name, cluster.Namespace, nodeName, nodeName, ip, token, version)
}

func (s *SharedAgent) Resources() ([]ctrlruntimeclient.Object, error) {
	// generate certs for webhook
	certSecret, err := s.webhookTLS()
	if err != nil {
		return nil, err
	}
	return []ctrlruntimeclient.Object{
		s.serviceAccount(),
		s.role(),
		s.roleBinding(),
		s.service(),
		s.deployment(),
		s.dnsService(),
		certSecret}, nil
}

func (s *SharedAgent) deployment() *apps.Deployment {
	selector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"cluster": s.cluster.Name,
			"type":    "agent",
			"mode":    "shared",
		},
	}

	return &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
			Labels:    selector.MatchLabels,
		},
		Spec: apps.DeploymentSpec{
			Selector: selector,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector.MatchLabels,
				},
				Spec: s.podSpec(selector),
			},
		},
	}
}

func (s *SharedAgent) podSpec(affinitySelector *metav1.LabelSelector) v1.PodSpec {
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
			{
				Name: "webhook-certs",
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: WebhookSecretName(s.cluster.Name),
						Items: []v1.KeyToPath{
							{
								Key:  "tls.crt",
								Path: "tls.crt",
							},
							{
								Key:  "tls.key",
								Path: "tls.key",
							},
							{
								Key:  "ca.crt",
								Path: "ca.crt",
							},
						},
					},
				},
			},
		},
		Containers: []v1.Container{
			{
				Name:            s.Name(),
				Image:           s.sharedAgentImage,
				ImagePullPolicy: v1.PullPolicy(s.sharedAgentImagePullPolicy),
				Resources: v1.ResourceRequirements{
					Limits: limit,
				},
				Args: []string{
					"--config",
					sharedKubeletConfigPath,
				},
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      "config",
						MountPath: "/opt/rancher/k3k/",
						ReadOnly:  false,
					},
					{
						Name:      "webhook-certs",
						MountPath: "/opt/rancher/k3k-webhook",
						ReadOnly:  false,
					},
				},
				Ports: []v1.ContainerPort{
					{
						Name:          "webhook-port",
						Protocol:      v1.ProtocolTCP,
						ContainerPort: 9443,
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
				{
					Name:       "webhook-server",
					Protocol:   v1.ProtocolTCP,
					Port:       9443,
					TargetPort: intstr.FromInt32(9443),
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
				APIGroups: []string{""},
				Resources: []string{"persistentvolumeclaims", "pods", "pods/log", "pods/exec", "secrets", "configmaps", "services"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"k3k.io"},
				Resources: []string{"clusters"},
				Verbs:     []string{"get", "watch", "list"},
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
	return controller.SafeConcatNameWithPrefix(s.cluster.Name, SharedNodeAgentName)
}

func (s *SharedAgent) DNSName() string {
	return controller.SafeConcatNameWithPrefix(s.cluster.Name, "kube-dns")
}

func (s *SharedAgent) webhookTLS() (*v1.Secret, error) {
	// generate CA CERT/KEY
	caKeyBytes, err := certutil.MakeEllipticPrivateKeyPEM()
	if err != nil {
		return nil, err
	}

	caKey, err := certutil.ParsePrivateKeyPEM(caKeyBytes)
	if err != nil {
		return nil, err
	}

	cfg := certutil.Config{
		CommonName: fmt.Sprintf("k3k-webhook-ca@%d", time.Now().Unix()),
	}

	caCert, err := certutil.NewSelfSignedCACert(cfg, caKey.(crypto.Signer))
	if err != nil {
		return nil, err
	}

	caCertBytes := certutil.EncodeCertPEM(caCert)
	// generate webhook cert bundle
	altNames := certs.AddSANs([]string{s.Name(), s.cluster.Name})
	webhookCert, webhookKey, err := certs.CreateClientCertKey(
		s.Name(), nil,
		&altNames, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, time.Hour*24*time.Duration(356),
		string(caCertBytes),
		string(caKeyBytes))
	if err != nil {
		return nil, err
	}
	return &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      WebhookSecretName(s.cluster.Name),
			Namespace: s.cluster.Namespace,
		},
		Data: map[string][]byte{
			"tls.crt": webhookCert,
			"tls.key": webhookKey,
			"ca.crt":  caCertBytes,
			"ca.key":  caKeyBytes,
		},
	}, nil
}

func WebhookSecretName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, "webhook")
}
