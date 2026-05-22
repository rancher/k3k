package request

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"syscall"
	"time"
)

var ErrServerNotReady = errors.New("server not ready")

// K3sConfigResponse is the subset of the response from k3s' /v1-k3s/config endpoint that we care about.
type K3sConfigResponse struct {
	ClusterInit bool `json:"clusterInit"`
}

// GetK3sConfig fetches the runtime configuration from a k3s server. It is used to detect
// whether the cluster is running with embedded etcd (ClusterInit=true) or an external datastore.
func GetK3sConfig(serviceIP, token string) (*K3sConfigResponse, error) {
	data, err := RequestK3sServer(serviceIP, "/v1-k3s/config", "node", token, nil)
	if err != nil {
		return nil, err
	}

	var cfg K3sConfigResponse
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func RequestK3sServer(serviceIP, endpoint, user, token string, extraHeaders map[string]string) ([]byte, error) {
	url := "https://" + serviceIP + endpoint

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

	req.Header.Add("Authorization", "Basic "+basicAuth(user, token))

	for headerName, headerValue := range extraHeaders {
		req.Header.Add(headerName, headerValue)
	}

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return nil, ErrServerNotReady
		}

		return nil, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	return io.ReadAll(resp.Body)
}
