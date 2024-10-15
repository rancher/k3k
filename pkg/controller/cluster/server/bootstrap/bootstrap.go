package bootstrap

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

type ControlRuntimeBootstrap struct {
	ServerCA        content `json:"serverCA"`
	ServerCAKey     content `json:"serverCAKey"`
	ClientCA        content `json:"clientCA"`
	ClientCAKey     content `json:"clientCAKey"`
	ETCDServerCA    content `json:"etcdServerCA"`
	ETCDServerCAKey content `json:"etcdServerCAKey"`
}

type content struct {
	Timestamp string
	Content   string
}

// Generate generates the bootstrap for the cluster:
// 1- use the server token to get the bootstrap data from k3s
// 2- save the bootstrap data as a secret
func Generate(ctx context.Context, cluster *v1alpha1.Cluster, ip string) (*v1.Secret, error) {
	token := cluster.Spec.Token

	var bootstrap *ControlRuntimeBootstrap
	if err := retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return true
	}, func() error {
		var err error
		bootstrap, err = requestBootstrap(token, ip)
		return err
	}); err != nil {
		return nil, err
	}

	if err := decodeBootstrap(bootstrap); err != nil {
		return nil, err
	}

	bootstrapData, err := json.Marshal(bootstrap)
	if err != nil {
		return nil, err
	}
	return &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-bootstrap",
			Namespace: util.ClusterNamespace(cluster),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: cluster.APIVersion,
					Kind:       cluster.Kind,
					Name:       cluster.Name,
					UID:        cluster.UID,
				},
			},
		},
		Data: map[string][]byte{
			"bootstrap": bootstrapData,
		},
	}, nil

}

func requestBootstrap(token, serverIP string) (*ControlRuntimeBootstrap, error) {
	url := "https://" + serverIP + ":6443/v1-k3s/server-bootstrap"

	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Basic "+basicAuth("server", token))

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var runtimeBootstrap ControlRuntimeBootstrap
	if err := json.NewDecoder(resp.Body).Decode(&runtimeBootstrap); err != nil {
		return nil, err
	}

	return &runtimeBootstrap, nil
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func decodeBootstrap(bootstrap *ControlRuntimeBootstrap) error {
	//client-ca
	decoded, err := base64.StdEncoding.DecodeString(bootstrap.ClientCA.Content)
	if err != nil {
		return err
	}
	bootstrap.ClientCA.Content = string(decoded)

	//client-ca-key
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ClientCAKey.Content)
	if err != nil {
		return err
	}
	bootstrap.ClientCAKey.Content = string(decoded)

	//server-ca
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ServerCA.Content)
	if err != nil {
		return err
	}
	bootstrap.ServerCA.Content = string(decoded)

	//server-ca-key
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ServerCAKey.Content)
	if err != nil {
		return err
	}
	bootstrap.ServerCAKey.Content = string(decoded)

	//etcd-ca
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ETCDServerCA.Content)
	if err != nil {
		return err
	}
	bootstrap.ETCDServerCA.Content = string(decoded)

	//etcd-ca-key
	decoded, err = base64.StdEncoding.DecodeString(bootstrap.ETCDServerCAKey.Content)
	if err != nil {
		return err
	}
	bootstrap.ETCDServerCAKey.Content = string(decoded)

	return nil
}

func DecodedBootstrap(token, ip string) (*ControlRuntimeBootstrap, error) {
	bootstrap, err := requestBootstrap(token, ip)
	if err != nil {
		return nil, err
	}

	if err := decodeBootstrap(bootstrap); err != nil {
		return nil, err
	}

	return bootstrap, nil
}
