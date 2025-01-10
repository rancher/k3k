package webhook

import (
	"context"
	"errors"
	"fmt"

	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/log"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	webhookName    = "nodename.podmutator.k3k.io"
	webhookTimeout = int32(10)
	webhookPort    = "9443"
	webhookPath    = "/mutate--v1-pod"
)

type webhookHandler struct {
	client           ctrlruntimeclient.Client
	scheme           *runtime.Scheme
	nodeName         string
	clusterName      string
	clusterNamespace string
	logger           *log.Logger
}

// AddPodMutatorWebhook will add a mutator webhook to the virtual cluster to
// modify the nodeName of the created pods with the name of the virtual kubelet node name
func AddPodMutatorWebhook(ctx context.Context, mgr manager.Manager, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace, nodeName string, logger *log.Logger) error {
	handler := webhookHandler{
		client:           mgr.GetClient(),
		scheme:           mgr.GetScheme(),
		logger:           logger,
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,
		nodeName:         nodeName,
	}

	// create mutator webhook configuration to the cluster
	config, err := handler.configuration(ctx, hostClient)
	if err != nil {
		return err
	}
	if err := handler.client.Create(ctx, config); err != nil {
		return err
	}
	// register webhook with the manager
	return ctrl.NewWebhookManagedBy(mgr).For(&v1.Pod{}).WithDefaulter(&handler).Complete()
}

func (w *webhookHandler) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return fmt.Errorf("invalid request: object was type %t not cluster", obj)
	}
	w.logger.Infow("recieved request", "Pod", pod.Name, "Namespace", pod.Namespace)
	if pod.Spec.NodeName == "" {
		pod.Spec.NodeName = w.nodeName
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
	webhookURL := "https://" + w.nodeName + ":" + webhookPort + webhookPath
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
