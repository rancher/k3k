package webhook

import (
	"context"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/controller"
)

const (
	webhookName = "podmutating.k3k.io"
)

func RemovePodMutatingWebhook(ctx context.Context, virtualClient, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace string) error {
	webhookSecret := &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      controller.SafeConcatNameWithPrefix(clusterName, "webhook"),
			Namespace: clusterNamespace,
		},
	}

	if err := hostClient.Delete(ctx, webhookSecret); !apierrors.IsNotFound(err) {
		return err
	}

	webhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admissionregistration.k8s.io/v1",
			Kind:       "MutatingWebhookConfiguration",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: webhookName + "-configuration",
		},
	}

	if err := virtualClient.Delete(ctx, webhook); !apierrors.IsNotFound(err) {
		return err
	}

	return nil
}
