// Package framework provides the shared helpers used by the k3k test suites:
// the host cluster clients, and the operations built on top of them.
//
// Most helpers use Ginkgo/Gomega assertions, so they must only be called from
// within a spec, after RegisterFailHandler has been called.
package framework

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/log"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// Framework bundles the host cluster clients needed by the test helpers.
// Every method acts on the host cluster, or on a virtual cluster created through it.
type Framework struct {
	HostIP     string
	RestConfig *rest.Config
	Clientset  *kubernetes.Clientset
	Client     ctrlruntimeclient.Client
}

// New initializes the host cluster clients from the KUBECONFIG environment variable.
// It also sets up the controller-runtime logger.
func New(ctx context.Context) (*Framework, error) {
	logger, err := zap.NewDevelopment()
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	log.SetLogger(zapr.NewLogger(logger))

	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		return nil, fmt.Errorf("KUBECONFIG environment variable is not set")
	}

	kubeconfig, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read kubeconfig from %s: %w", kubeconfigPath, err)
	}

	return newFromBytes(kubeconfig)
}

// newFromBytes initializes the host cluster clients from kubeconfig bytes.
func newFromBytes(kubeconfig []byte) (*Framework, error) {
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config: %w", err)
	}

	hostIP, err := getServerIP(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get server IP: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	runtimeClient, err := ctrlruntimeclient.New(restConfig, ctrlruntimeclient.Options{Scheme: NewScheme()})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}

	return &Framework{
		RestConfig: restConfig,
		Clientset:  clientset,
		Client:     runtimeClient,
		HostIP:     hostIP,
	}, nil
}

// NewScheme creates a new Kubernetes runtime scheme with core APIs and k3k CRDs.
// This is suitable for most k3k test scenarios including integration and E2E tests.
func NewScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	// Add core Kubernetes scheme (includes most common types)
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}

	// Add k3k CRDs
	if err := v1beta1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	return scheme
}

// getServerIP extracts the server IP by parsing the hostname from the REST config host.
func getServerIP(cfg *rest.Config) (string, error) {
	u, err := url.Parse(cfg.Host)
	if err != nil {
		return "", fmt.Errorf("failed to parse REST config host: %w", err)
	}

	host := u.Hostname()

	if isLoopbackHost(host) {
		if ip, ok := firstNonLoopbackIPv4(); ok {
			return ip, nil
		}
	}

	return host, nil
}

func isLoopbackHost(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}

	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}

	return false
}

func firstNonLoopbackIPv4() (string, bool) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", false
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		ip := ipNet.IP.To4()
		if ip == nil || ip.IsLoopback() || !ip.IsGlobalUnicast() {
			continue
		}

		return ip.String(), true
	}

	return "", false
}
