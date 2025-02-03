package agent

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	sharedKubeletConfigPath = "/opt/rancher/k3k/config.yaml"
	SharedNodeAgentName     = "kubelet"
	SharedNodeMode          = "shared"
)

type SharedAgent struct {
	cluster         *v1alpha1.Cluster
	client          ctrlruntimeclient.Client
	scheme          *runtime.Scheme
	serviceIP       string
	image           string
	imagePullPolicy string
	token           string
}

func NewSharedAgent(cluster *v1alpha1.Cluster, client ctrlruntimeclient.Client, scheme *runtime.Scheme, serviceIP, image, imagePullPolicy, token string) *SharedAgent {
	return &SharedAgent{
		cluster:         cluster,
		client:          client,
		scheme:          scheme,
		serviceIP:       serviceIP,
		image:           image,
		imagePullPolicy: imagePullPolicy,
		token:           token,
	}
}

func (s *SharedAgent) EnsureResources() error {
	if err := errors.Join(
		s.config(),
		s.serviceAccount(),
		s.role(),
		s.roleBinding(),
		s.service(),
		s.deployment(),
		s.dnsService(),
		s.webhookTLS(),
	); err != nil {
		return fmt.Errorf("failed to ensure some kubelet resources: %w\n", err)
	}

	return nil
}

func (s *SharedAgent) config() error {
	config := sharedAgentData(s.cluster, s.Name(), s.token, s.serviceIP)

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

	return s.ensureObject(context.Background(), configSecret)
}

func sharedAgentData(cluster *v1alpha1.Cluster, serviceName, token, ip string) string {
	version := cluster.Spec.Version
	if cluster.Spec.Version == "" {
		version = cluster.Status.HostVersion
	}
	return fmt.Sprintf(`clusterName: %s
clusterNamespace: %s
serverIP: %s
serviceName: %s
token: %s
version: %s`,
		cluster.Name, cluster.Namespace, ip, serviceName, token, version)
}

func (s *SharedAgent) deployment() error {
	labels := map[string]string{
		"cluster": s.cluster.Name,
		"type":    "agent",
		"mode":    "shared",
	}

	deploy := &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name(),
			Namespace: s.cluster.Namespace,
			Labels:    labels,
		},
		Spec: apps.DeploymentSpec{
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

	return s.ensureObject(context.Background(), deploy)
}

func (s *SharedAgent) podSpec() v1.PodSpec {
	var limit v1.ResourceList

	return v1.PodSpec{
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
				Image:           s.image,
				ImagePullPolicy: v1.PullPolicy(s.imagePullPolicy),
				Resources: v1.ResourceRequirements{
					Limits: limit,
				},
				Args: []string{
					"--config",
					sharedKubeletConfigPath,
				},
				Env: []v1.EnvVar{
					{
						Name: "AGENT_HOSTNAME",
						ValueFrom: &v1.EnvVarSource{
							FieldRef: &v1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "spec.nodeName",
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
		},
	}
}

func (s *SharedAgent) service() error {
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

	return s.ensureObject(context.Background(), svc)
}

func (s *SharedAgent) dnsService() error {
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

	return s.ensureObject(context.Background(), svc)
}

func (s *SharedAgent) serviceAccount() error {
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

	return s.ensureObject(context.Background(), svcAccount)
}

func (s *SharedAgent) role() error {
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

	return s.ensureObject(context.Background(), role)
}

func (s *SharedAgent) roleBinding() error {
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

	return s.ensureObject(context.Background(), roleBinding)
}

func (s *SharedAgent) Name() string {
	return controller.SafeConcatNameWithPrefix(s.cluster.Name, SharedNodeAgentName)
}

func (s *SharedAgent) webhookTLS() error {
	ctx := context.Background()

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

	key := client.ObjectKeyFromObject(webhookSecret)
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
