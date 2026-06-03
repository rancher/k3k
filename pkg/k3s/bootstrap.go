package k3s

import (
	"encoding/base64"
	"fmt"
	"net/http"
)

type BootstrapData struct {
	ServerCA        cert `json:"serverCA"`
	ServerCAKey     cert `json:"serverCAKey"`
	ClientCA        cert `json:"clientCA"`
	ClientCAKey     cert `json:"clientCAKey"`
	ETCDServerCA    cert `json:"etcdServerCA"`
	ETCDServerCAKey cert `json:"etcdServerCAKey"`
}

type cert struct {
	Timestamp string
	Content   string
}

func GetServerBootstrap(c *Client) (*BootstrapData, error) {
	endpoint := "/v1-k3s/server-bootstrap"

	bootstrap, err := do[BootstrapData](c, endpoint, "server", http.MethodGet)
	if err != nil {
		return nil, err
	}

	// we still need to decode each certs since the bootstrap data endpoint base64 encode each cert
	if err := decode(&bootstrap); err != nil {
		return nil, fmt.Errorf("failed to decode bootstrap secret: %w", err)
	}

	return &bootstrap, nil
}

func decode(data *BootstrapData) error {
	// client-ca
	decoded, err := base64.StdEncoding.DecodeString(data.ClientCA.Content)
	if err != nil {
		return err
	}

	data.ClientCA.Content = string(decoded)

	// client-ca-key
	decoded, err = base64.StdEncoding.DecodeString(data.ClientCAKey.Content)
	if err != nil {
		return err
	}

	data.ClientCAKey.Content = string(decoded)

	// server-ca
	decoded, err = base64.StdEncoding.DecodeString(data.ServerCA.Content)
	if err != nil {
		return err
	}

	data.ServerCA.Content = string(decoded)

	// server-ca-key
	decoded, err = base64.StdEncoding.DecodeString(data.ServerCAKey.Content)
	if err != nil {
		return err
	}

	data.ServerCAKey.Content = string(decoded)

	// etcd-ca
	decoded, err = base64.StdEncoding.DecodeString(data.ETCDServerCA.Content)
	if err != nil {
		return err
	}

	data.ETCDServerCA.Content = string(decoded)

	// etcd-ca-key
	decoded, err = base64.StdEncoding.DecodeString(data.ETCDServerCAKey.Content)
	if err != nil {
		return err
	}

	data.ETCDServerCAKey.Content = string(decoded)

	return nil
}
