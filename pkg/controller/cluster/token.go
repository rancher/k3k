package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (c *ClusterReconciler) token(ctx context.Context, cluster *v1alpha1.Cluster) (string, error) {
	if cluster.Spec.TokenSecretRef == nil {
		return c.ensureTokenSecret(ctx, cluster)
	}
	// get token data from secretRef
	nn := types.NamespacedName{
		Name:      cluster.Spec.TokenSecretRef.Name,
		Namespace: cluster.Spec.TokenSecretRef.Namespace,
	}
	var tokenSecret v1.Secret
	if err := c.Client.Get(ctx, nn, &tokenSecret); err != nil {
		return "", err
	}
	if _, ok := tokenSecret.Data["token"]; !ok {
		return "", fmt.Errorf("no token field in secret %s/%s", nn.Namespace, nn.Name)
	}
	return string(tokenSecret.Data["token"]), nil
}

func (c *ClusterReconciler) ensureTokenSecret(ctx context.Context, cluster *v1alpha1.Cluster) (string, error) {
	// check if the secret is already created
	var (
		tokenSecret v1.Secret
		nn          = types.NamespacedName{
			Name:      TokenSecretName(cluster.Name),
			Namespace: cluster.Namespace,
		}
	)
	if err := c.Client.Get(ctx, nn, &tokenSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return "", err
		}
	}

	if tokenSecret.Data != nil {
		return string(tokenSecret.Data["token"]), nil
	}
	c.logger.Info("Token secret is not specified, creating a random token")
	token, err := random(16)
	if err != nil {
		return "", err
	}
	tokenSecret = TokenSecretObj(token, cluster.Name, cluster.Namespace)
	if err := controllerutil.SetControllerReference(cluster, &tokenSecret, c.Scheme); err != nil {
		return "", err
	}
	if err := c.ensure(ctx, &tokenSecret, false); err != nil {
		return "", err
	}
	return token, nil

}

func random(size int) (string, error) {
	token := make([]byte, size)
	_, err := rand.Read(token)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(token), err
}

func TokenSecretObj(token, name, namespace string) v1.Secret {
	return v1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      TokenSecretName(name),
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"token": []byte(token),
		},
	}
}

func TokenSecretName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, "token")
}
