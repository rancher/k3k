package bootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/k3s"
)

const (
	TLSDir = "/var/lib/rancher/k3s/server/tls/"
)

// Fetch requests bootstrap data from k3s using the token and decodes it,
// to avoid double encoding when stored as secret.
func Fetch(ctx context.Context, ip, token string) (*k3s.BootstrapData, error) {
	log := ctrl.LoggerFrom(ctx)

	client := k3s.New(k3s.ClientConfig{
		ServerIP: ip,
		Token:    token,
	})

	k3sConfig, err := k3s.GetServerConfig(client)
	if err != nil {
		return nil, err
	}

	if k3sConfig.ClusterInit {
		log.V(1).Info("Fetching bootstrap data from K3s API")
		return fetchFromK3sAPI(ip, token)
	}

	log.V(1).Info("Fetching bootstrap data from K3s server Pod")
	return fetchFromK3sPod()

}

// SaveToSecret marshals the bootstrap data and stores it in a Secret owned by the cluster,
// creating the Secret if it does not exist or updating it otherwise.
func SaveToSecret(ctx context.Context, c client.Client, scheme *runtime.Scheme, cluster *v1beta1.Cluster, data *k3s.BootstrapData) error {
	bootstrapData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controller.SafeConcatNameWithPrefix(cluster.Name, "bootstrap"),
			Namespace: cluster.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, c, secret, func() error {
		if err := controllerutil.SetControllerReference(cluster, secret, scheme); err != nil {
			return err
		}

		secret.Data = map[string][]byte{
			"bootstrap": bootstrapData,
		}

		return nil
	})

	return err
}

// LoadFromSecret reads the bootstrap data of a certain cluster and returns the decoded content.
func LoadFromSecret(ctx context.Context, client client.Client, cluster *v1beta1.Cluster) (*k3s.BootstrapData, error) {
	key := types.NamespacedName{
		Name:      controller.SafeConcatNameWithPrefix(cluster.Name, "bootstrap"),
		Namespace: cluster.Namespace,
	}

	var bootstrapSecret corev1.Secret
	if err := client.Get(ctx, key, &bootstrapSecret); err != nil {
		return nil, err
	}

	bootstrapData := bootstrapSecret.Data["bootstrap"]
	if bootstrapData == nil {
		return nil, errors.New("empty bootstrap")
	}

	var bootstrap k3s.BootstrapData

	err := json.Unmarshal(bootstrapData, &bootstrap)

	return &bootstrap, err
}

func fetchFromK3sAPI(serviceIP, token string) (*k3s.BootstrapData, error) {
	client := k3s.New(k3s.ClientConfig{
		ServerIP: serviceIP,
		Token:    token,
	})

	return k3s.GetServerBootstrap(client)
}

func fetchFromK3sPod() (*k3s.BootstrapData, error) {
	return k3s.GetServerBootstrap(client)
}
