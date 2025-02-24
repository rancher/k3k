package webhook

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/log"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	webhookName    = "podmutator.k3k.io"
	webhookTimeout = int32(10)
	webhookPort    = "9443"
	webhookPath    = "/mutate--v1-pod"
	FieldpathField = "k3k.io/fieldpath"
)

type webhookHandler struct {
	client           ctrlruntimeclient.Client
	scheme           *runtime.Scheme
	serviceName      string
	clusterName      string
	clusterNamespace string
	logger           *log.Logger
}

// AddPodMutatorWebhook will add a mutator webhook to the virtual cluster to
// modify the nodeName of the created pods with the name of the virtual kubelet node name
// as well as remove any status fields of the downward apis env fields
func AddPodMutatorWebhook(ctx context.Context, mgr manager.Manager, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace, serviceName string, logger *log.Logger) error {
	handler := webhookHandler{
		client:           mgr.GetClient(),
		scheme:           mgr.GetScheme(),
		logger:           logger,
		serviceName:      serviceName,
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,
	}

	// create mutator webhook configuration to the cluster
	config, err := handler.configuration(ctx, hostClient)
	if err != nil {
		return err
	}
	if err := handler.client.Create(ctx, config); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}
	// register webhook with the manager
	return ctrl.NewWebhookManagedBy(mgr).For(&v1.Pod{}).WithDefaulter(&handler).Complete()
}

func (w *webhookHandler) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return fmt.Errorf("invalid request: object was type %t not cluster", obj)
	}
	w.logger.Infow("mutator webhook request", "Pod", pod.Name, "Namespace", pod.Namespace)
	// look for status.* fields in the env
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	for i, container := range pod.Spec.Containers {
		for j, env := range container.Env {
			if env.ValueFrom == nil || env.ValueFrom.FieldRef == nil {
				continue
			}

			fieldPath := env.ValueFrom.FieldRef.FieldPath
			if strings.Contains(fieldPath, "status.") {
				annotationKey := fmt.Sprintf("%s_%d_%s", FieldpathField, i, env.Name)
				pod.Annotations[annotationKey] = fieldPath
				pod.Spec.Containers[i].Env = removeEnv(pod.Spec.Containers[i].Env, j)
			}
		}
	}
	return nil
}

func (w *webhookHandler) configuration(ctx context.Context, hostClient ctrlruntimeclient.Client) (*admissionregistrationv1.MutatingWebhookConfiguration, error) {
	w.logger.Infow("extracting webhook tls from host cluster")
	var (
		webhookTLSSecret v1.Secret
	)
	if err := hostClient.Get(ctx, types.NamespacedName{Name: agent.WebhookSecretName(w.clusterName), Namespace: w.clusterNamespace}, &webhookTLSSecret); err != nil {
		return nil, err
	}
	caBundle, ok := webhookTLSSecret.Data["ca.crt"]
	if !ok {
		return nil, errors.New("webhook CABundle does not exist in secret")
	}
	webhookURL := "https://" + w.serviceName + ":" + webhookPort + webhookPath
	return &admissionregistrationv1.MutatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admissionregistration.k8s.io/v1",
			Kind:       "MutatingWebhookConfiguration",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: webhookName + "-configuration",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name:                    webhookName,
				AdmissionReviewVersions: []string{"v1"},
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				TimeoutSeconds:          ptr.To(webhookTimeout),
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					URL:      ptr.To(webhookURL),
					CABundle: caBundle,
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							"CREATE",
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
							Scope:       ptr.To(admissionregistrationv1.NamespacedScope),
						},
					},
				},
			},
		},
	}, nil
}

func removeEnv(envs []v1.EnvVar, i int) []v1.EnvVar {
	envs[i] = envs[len(envs)-1]
	return envs[:len(envs)-1]
}

func ParseFieldPathAnnotationKey(annotationKey string) (int, string, error) {
	s := strings.SplitN(annotationKey, "_", 3)
	if len(s) != 3 {
		return -1, "", errors.New("fieldpath annotation is not set correctly")
	}
	containerIndex, err := strconv.Atoi(s[1])
	if err != nil {
		return -1, "", err
	}
	envName := s[2]

	return containerIndex, envName, nil
}
