package cluster

import (
	"context"
	"fmt"
	"reflect"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *ClusterReconciler) customCerts(ctx context.Context, cluster *v1alpha1.Cluster) error {
	if cluster.Spec.CustomCertificates.SecretName != "" {
		return nil
	}

	// validate custom certificates content if secretName is not provided.
	if err := validateCustomCerts(cluster.Spec.CustomCertificates.Content); err != nil {
		return err
	}

	customCertsSecret, err := secret(cluster, cluster.Spec.CustomCertificates.Content)
	if err != nil {
		return err
	}

	return c.Client.Create(ctx, customCertsSecret)
}

// validateCustomCerts will make sure that each field in customCertificatesContent is not empty.
func validateCustomCerts(customCerts v1alpha1.CustomCertificatesContent) error {
	t := reflect.TypeOf(customCerts)
	for i := 0; i < t.NumField(); i++ {
		crtPair := t.Field(i)

		crtKey := reflect.ValueOf(customCerts).FieldByName(crtPair.Name)

		// Get reflect value of the struct itself without using pointer to it.
		for j := 0; j < crtKey.NumField(); j++ {
			fieldVal := crtKey.Field(j).String()
			if fieldVal == "" {
				return fmt.Errorf("%s crt or key is empty", crtPair.Name)
			}
		}

	}
	return nil
}

func secret(cluster *v1alpha1.Cluster, customCerts v1alpha1.CustomCertificatesContent) (*v1.Secret, error) {
	return &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      controller.SafeConcatNameWithPrefix(cluster.Name, "custom", "certs"),
			Namespace: cluster.Namespace,
		},
		Data: map[string][]byte{
			"server-ca.crt":         []byte(customCerts.ServerCA.Certificate),
			"server-ca.key":         []byte(customCerts.ServerCA.Key),
			"client-ca.crt":         []byte(customCerts.ClientCA.Certificate),
			"client-ca.key":         []byte(customCerts.ClientCA.Key),
			"request-header-ca.crt": []byte(customCerts.RequestHeaderCA.Certificate),
			"request-header-ca.key": []byte(customCerts.RequestHeaderCA.Key),
			"etcd-peer-ca.crt":      []byte(customCerts.ETCDPeerCA.Certificate),
			"etcd-peer-ca.key":      []byte(customCerts.ETCDPeerCA.Key),
			"etcd-server-ca.crt":    []byte(customCerts.ETCDServerCA.Certificate),
			"etcd-server-ca.key":    []byte(customCerts.ETCDServerCA.Key),
			"service.key":           []byte(customCerts.ServiceAccountToken.Key),
		},
	}, nil
}
