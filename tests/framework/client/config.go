package client

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/go-logr/zapr"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Config holds the Kubernetes client configuration and clients.
type Config struct {
	RestConfig *rest.Config
	Clientset  *kubernetes.Clientset
	Client     client.Client
	HostIP     string
}

// InitFromKubeconfig initializes Kubernetes clients from the KUBECONFIG environment variable.
// It sets up logging, reads the kubeconfig file, creates REST config and clients.
// The scheme parameter should be created using the scheme package.
func InitFromKubeconfig(ctx context.Context, scheme *runtime.Scheme, k3sContainer *k3s.K3sContainer) (*Config, error) {
	// Setup logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	log.SetLogger(zapr.NewLogger(logger))

	// Get kubeconfig path from environment
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		return nil, fmt.Errorf("KUBECONFIG environment variable is not set")
	}

	// Read kubeconfig file
	kubeconfig, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read kubeconfig from %s: %w", kubeconfigPath, err)
	}

	return InitFromBytes(ctx, kubeconfig, scheme, k3sContainer)
}

// InitFromBytes initializes Kubernetes clients from kubeconfig bytes.
// The scheme parameter should be created using the scheme package.
func InitFromBytes(ctx context.Context, kubeconfig []byte, scheme *runtime.Scheme, k3sContainer *k3s.K3sContainer) (*Config, error) {
	// Create REST config from kubeconfig
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config: %w", err)
	}

	// Extract host IP from REST config
	hostIP, err := getServerIP(ctx, restConfig, k3sContainer)
	if err != nil {
		return nil, fmt.Errorf("failed to get server IP: %w", err)
	}

	// Create Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Create controller-runtime client
	runtimeClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}

	return &Config{
		RestConfig: restConfig,
		Clientset:  clientset,
		Client:     runtimeClient,
		HostIP:     hostIP,
	}, nil
}

// getServerIP extracts the server IP from the REST config.
// If running with testcontainers, it returns the container IP.
// Otherwise, it parses the hostname from the REST config host.
func getServerIP(ctx context.Context, cfg *rest.Config, k3sContainer *k3s.K3sContainer) (string, error) {
	if k3sContainer != nil {
		return k3sContainer.ContainerIP(ctx)
	}

	u, err := url.Parse(cfg.Host)
	if err != nil {
		return "", fmt.Errorf("failed to parse REST config host: %w", err)
	}

	// If Host includes a port, u.Hostname() extracts just the hostname part
	return u.Hostname(), nil
}
