package agent

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"

	certutil "github.com/rancher/dynamiclistener/cert"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
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
	webhookPort      int
	imagePullSecrets []string
}

func NewSharedAgent(config *Config, serviceIP, image, imagePullPolicy, imageRegistry, token string, kubeletPort, webhookPort int, imagePullSecrets []string) *SharedAgent {
	return &SharedAgent{
		Config:           config,
		serviceIP:        serviceIP,
		image:            image,
		imagePullPolicy:  imagePullPolicy,
		imageRegistry:    imageRegistry,
		token:            token,
		kubeletPort:      kubeletPort,
		webhookPort:      webhookPort,
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
		s.webhookTLS(ctx),
	); err != nil {
		return fmt.Errorf("failed to ensure some resources: %w", err)
	}

	return nil
}

func (s *SharedAgent) ensureObject(ctx context.Context, obj ctrlruntimeclient.Object) error {
	return ensureObject(ctx, s.Config, obj)
}

func (s *SharedAgent) config(ctx context.Context) error {
	config := sharedAgentData(s.cluster, s.Name(), s.token, s.serviceIP, s.kubeletPort, s.webhookPort)

	configSecret := &v1.Secret{
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

func sharedAgentData(cluster *v1alpha1.Cluster, serviceName, token, ip string, kubeletPort, webhookPort int) string {
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
webhookPort: %d
kubeletPort: %d`,
		cluster.Name, cluster.Namespace, ip, serviceName, token, cluster.Spec.MirrorHostNodes, version, webhookPort, kubeletPort)
}

func (s *SharedAgent) daemonset(ctx context.Context) error {
	labels := map[string]string{
		"cluster": s.cluster.Name,
		"type":    "agent",
		"mode":    "shared",
	}

	deploy := &apps.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
			Labels:    labels,
		},
		Spec: apps.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: s.podSpec(),
			},
		},
	}

	return s.ensureObject(ctx, deploy)
}

func (s *SharedAgent) podSpec() v1.PodSpec {
	hostNetwork := false
	dnsPolicy := v1.DNSClusterFirst

	if s.cluster.Spec.MirrorHostNodes {
		hostNetwork = true
		dnsPolicy = v1.DNSClusterFirstWithHostNet
	}
	image := s.image
	if s.imageRegistry != "" {
		image = s.imageRegistry + "/" + s.image
	}

	podSpec := v1.PodSpec{
		HostNetwork:        hostNetwork,
		DNSPolicy:          dnsPolicy,
		ServiceAccountName: s.Name(),
		NodeSelector:       s.cluster.Spec.NodeSelector,
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
				Image:           image,
				ImagePullPolicy: v1.PullPolicy(s.imagePullPolicy),
				Resources: v1.ResourceRequirements{
					Limits: v1.ResourceList{},
				},
				Env: append([]v1.EnvVar{
					{
						Name: "AGENT_HOSTNAME",
						ValueFrom: &v1.EnvVarSource{
							FieldRef: &v1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "spec.nodeName",
							},
						},
					},
					{
						Name: "POD_IP",
						ValueFrom: &v1.EnvVarSource{
							FieldRef: &v1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "status.podIP",
							},
						},
					},
				}, s.cluster.Spec.AgentEnvs...),
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
						Name:          "kubelet-port",
						Protocol:      v1.ProtocolTCP,
						ContainerPort: int32(s.kubeletPort),
					},
					{
						Name:          "webhook-port",
						Protocol:      v1.ProtocolTCP,
						ContainerPort: int32(s.webhookPort),
					},
				},
			},
		},
	}
	for _, imagePullSecret := range s.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, v1.LocalObjectReference{Name: imagePullSecret})
	}

	return podSpec
}

func (s *SharedAgent) service(ctx context.Context) error {
	svc := &v1.Service{
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
					Port:     int32(s.kubeletPort),
				},
				{
					Name:       "webhook-server",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(s.webhookPort),
					TargetPort: intstr.FromInt32(int32(s.webhookPort)),
				},
			},
		},
	}

	return s.ensureObject(ctx, svc)
}

func (s *SharedAgent) dnsService(ctx context.Context) error {
	dnsServiceName := controller.SafeConcatNameWithPrefix(s.cluster.Name, "kube-dns")

	svc := &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      dnsServiceName,
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

	return s.ensureObject(ctx, svc)
}

func (s *SharedAgent) serviceAccount(ctx context.Context) error {
	svcAccount := &v1.ServiceAccount{
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

func (s *SharedAgent) webhookTLS(ctx context.Context) error {
	webhookSecret := &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      WebhookSecretName(s.cluster.Name),
			Namespace: s.cluster.Namespace,
		},
	}

	key := ctrlruntimeclient.ObjectKeyFromObject(webhookSecret)
	if err := s.client.Get(ctx, key, webhookSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		caPrivateKeyPEM, caCertPEM, err := newWebhookSelfSignedCACerts()
		if err != nil {
			return err
		}

		altNames := []string{s.Name(), s.cluster.Name}

		webhookCert, webhookKey, err := newWebhookCerts(s.Name(), altNames, caPrivateKeyPEM, caCertPEM)
		if err != nil {
			return err
		}

		webhookSecret.Data = map[string][]byte{
			"tls.crt": webhookCert,
			"tls.key": webhookKey,
			"ca.crt":  caCertPEM,
			"ca.key":  caPrivateKeyPEM,
		}

		return s.ensureObject(ctx, webhookSecret)
	}

	// if the webhook secret is found we can skip
	// we should check for their validity
	return nil
}

func newWebhookSelfSignedCACerts() ([]byte, []byte, error) {
	// generate CA CERT/KEY
	caPrivateKeyPEM, err := certutil.MakeEllipticPrivateKeyPEM()
	if err != nil {
		return nil, nil, err
	}

	caPrivateKey, err := certutil.ParsePrivateKeyPEM(caPrivateKeyPEM)
	if err != nil {
		return nil, nil, err
	}

	cfg := certutil.Config{
		CommonName: fmt.Sprintf("k3k-webhook-ca@%d", time.Now().Unix()),
	}

	caCert, err := certutil.NewSelfSignedCACert(cfg, caPrivateKey.(crypto.Signer))
	if err != nil {
		return nil, nil, err
	}

	caCertPEM := certutil.EncodeCertPEM(caCert)

	return caPrivateKeyPEM, caCertPEM, nil
}

func newWebhookCerts(commonName string, subAltNames []string, caPrivateKey, caCert []byte) ([]byte, []byte, error) {
	// generate webhook cert bundle
	altNames := certs.AddSANs(subAltNames)
	oneYearExpiration := time.Until(time.Now().AddDate(1, 0, 0))

	return certs.CreateClientCertKey(
		commonName,
		nil,
		&altNames,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		oneYearExpiration,
		string(caCert),
		string(caPrivateKey),
	)
}

func WebhookSecretName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, "webhook")
}
