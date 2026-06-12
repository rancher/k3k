package k3s

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"time"
)

type ClientConfig struct {
	AgentIP  string
	NodeName string
	PodIP    string
	ServerIP string
	Token    string
}
type Client struct {
	config        ClientConfig
	httpClient    *http.Client
	staticHeaders http.Header
}

var ErrServerNotReady = errors.New("server not ready")

const (
	k3sNodePasswordHeader = "k3s-Node-Password"
	k3sNodeIPHeader       = "k3s-Node-IP"
	k3sNodeNameHeader     = "k3s-Node-Name"
)

func New(config ClientConfig) *Client {
	httpClient := &http.Client{
		Transport: http.DefaultTransport,
		Timeout:   5 * time.Second,
	}

	// skip TLS verify for k3s server
	if transport, ok := httpClient.Transport.(*http.Transport); ok {
		transport.TLSClientConfig = &tls.Config{
			// This is insecure because the K3s CA hasn't been setup yet.
			InsecureSkipVerify: true,
		}
	}

	headers := http.Header{}

	if config.Token != "" {
		headers.Set(k3sNodePasswordHeader, config.Token)
	}

	if config.NodeName != "" {
		headers.Set(k3sNodeNameHeader, config.NodeName)
	}

	var nodeIPs []string
	if config.AgentIP != "" {
		nodeIPs = append(nodeIPs, config.AgentIP)
	}

	if config.PodIP != "" {
		nodeIPs = append(nodeIPs, config.PodIP)
	}

	if len(nodeIPs) > 0 {
		headers.Set("k3s-Node-IP", strings.Join(nodeIPs, ","))
	}

	return &Client{
		httpClient:    httpClient,
		config:        config,
		staticHeaders: headers,
	}
}

func do[T any](c *Client, endpoint, user, method string) (*T, error) {
	response := new(T)

	respBody, err := c.do(endpoint, user, method)
	if err != nil {
		return nil, err
	}

	// unmarshal the json data to the generic struct
	if err := json.Unmarshal(respBody, response); err != nil {
		return nil, err
	}

	return response, nil
}

func (c *Client) do(endpoint, user, method string) ([]byte, error) {
	url := "https://" + c.config.ServerIP + endpoint

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(user, c.config.Token)

	for headerName, headerValues := range c.staticHeaders {
		for _, headerValue := range headerValues {
			req.Header.Add(headerName, headerValue)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return nil, ErrServerNotReady
		}

		return nil, err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("failed executing '%s' request to k3s server: status code: %s", endpoint, resp.Status)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	return io.ReadAll(resp.Body)
}
