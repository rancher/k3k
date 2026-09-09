package k3s

import (
	"crypto/tls"
	"net/http"
)

// GetServingKubeletCrt returns the serving certificate the k3s server issues for the kubelet.
func (c *Client) GetServingKubeletCrt() (*tls.Certificate, error) {
	endpoint := "/v1-k3s/serving-kubelet.crt"

	tlsCrtData, err := c.do(endpoint, "node", http.MethodGet, nil)
	if err != nil {
		return nil, err
	}

	tlsCrt, err := tls.X509KeyPair(tlsCrtData, tlsCrtData)
	if err != nil {
		return nil, err
	}

	return &tlsCrt, nil
}
