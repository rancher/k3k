package k3s

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func do[T any](c *Client, endpoint, user, method string, body any) (T, error) {
	var (
		response T
		reader   io.Reader
	)

	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return response, err
		}

		reader = bytes.NewReader(b)
	}

	respBody, err := c.do(endpoint, user, method, reader)
	if err != nil {
		return response, err
	}

	// unmarshal the json data to the generic struct
	if err := json.Unmarshal(respBody, &response); err != nil {
		return response, err
	}

	return response, nil
}

func (c *Client) do(endpoint, user, method string, reader io.Reader) ([]byte, error) {
	url := "https://" + c.config.ServerIP + endpoint

	req, err := http.NewRequest(method, url, reader)
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
		return nil, fmt.Errorf("failed executing '%s' request to k3s server: %w", endpoint, statusError(resp))
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	return io.ReadAll(resp.Body)
}

// statusError return back the error from failed requests
func statusError(resp *http.Response) error {
	var status metav1.Status

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, &status); err != nil {
		return fmt.Errorf("failed to unmarshal response body for failed request: %w", err)
	}

	if status.Status != metav1.StatusFailure {
		return fmt.Errorf("error status did not match failed request status")

	}

	return &apierrors.StatusError{ErrStatus: status}
}
