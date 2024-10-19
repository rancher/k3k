package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/k3k-kubelet/provider"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
}

type kubelet struct {
	name       string
	port       int
	hostConfig *rest.Config
	hostClient ctrlruntimeclient.Client
	virtClient kubernetes.Interface
	node       *nodeutil.Node
}

func newKubelet(ctx context.Context, c *config) (*kubelet, error) {
	hostConfig, err := clientcmd.BuildConfigFromFlags("", c.HostConfigPath)
	if err != nil {
		return nil, err
	}

	hostClient, err := ctrlruntimeclient.New(hostConfig, ctrlruntimeclient.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, err
	}

	virtConfig, err := virtRestConfig(ctx, c.VirtualConfigPath, hostClient, c.ClusterName, c.ClusterNamespace)
	if err != nil {
		return nil, err
	}

	virtClient, err := kubernetes.NewForConfig(virtConfig)
	if err != nil {
		return nil, err
	}
	return &kubelet{
		name:       c.NodeName,
		hostConfig: hostConfig,
		hostClient: hostClient,
		virtClient: virtClient,
	}, nil
}

func (k *kubelet) registerNode(ctx context.Context, srvPort, namespace, name, hostname string) error {
	providerFunc := k.newProviderFunc(namespace, name, hostname)
	nodeOpts := k.nodeOpts(ctx, srvPort, namespace, name, hostname)

	var err error
	k.node, err = nodeutil.NewNode(k.name, providerFunc, nodeutil.WithClient(k.virtClient), nodeOpts)
	if err != nil {
		return fmt.Errorf("unable to start kubelet: %v", err)
	}
	return nil
}

func (k *kubelet) start(ctx context.Context) {
	go func() {
		logger, err := zap.NewProduction()
		if err != nil {
			fmt.Println("unable to create logger:", err.Error())
			os.Exit(1)
		}
		wrapped := LogWrapper{
			*logger.Sugar(),
		}
		ctx = log.WithLogger(ctx, &wrapped)
		if err := k.node.Run(ctx); err != nil {
			fmt.Printf("node errored when running: %s \n", err.Error())
			os.Exit(1)
		}
	}()
	if err := k.node.WaitReady(context.Background(), time.Minute*1); err != nil {
		fmt.Println("node was not ready within timeout of 1 minute:", err.Error())
		os.Exit(1)
	}
	<-k.node.Done()
	if err := k.node.Err(); err != nil {
		fmt.Println("node stopped with an error:", err.Error())
		os.Exit(1)
	}
	fmt.Println("node exited without an error")
}

func (k *kubelet) newProviderFunc(namespace, name, hostname string) nodeutil.NewProviderFunc {
	return func(pc nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		utilProvider, err := provider.New(*k.hostConfig, namespace, name)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to make nodeutil provider %w", err)
		}
		nodeProvider := provider.Node{}

		provider.ConfigureNode(pc.Node, hostname, k.port)
		return utilProvider, &nodeProvider, nil
	}
}

func (k *kubelet) nodeOpts(ctx context.Context, srvPort, namespace, name, hostname string) nodeutil.NodeOpt {
	return func(c *nodeutil.NodeConfig) error {
		c.HTTPListenAddr = fmt.Sprintf(":%s", srvPort)
		// set up the routes
		mux := http.NewServeMux()
		if err := nodeutil.AttachProviderRoutes(mux)(c); err != nil {
			return fmt.Errorf("unable to attach routes: %w", err)
		}
		c.Handler = mux

		tlsConfig, err := loadTLSConfig(ctx, k.hostClient, name, namespace, k.name, hostname)
		if err != nil {
			return fmt.Errorf("unable to get tls config: %w", err)
		}
		c.TLSConfig = tlsConfig
		return nil
	}
}

func virtRestConfig(ctx context.Context, virtualConfigPath string, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace string) (*rest.Config, error) {
	if virtualConfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", virtualConfigPath)
	}
	// virtual kubeconfig file is empty, trying to fetch the k3k cluster kubeconfig
	var cluster v1alpha1.Cluster
	if err := hostClient.Get(ctx, types.NamespacedName{Namespace: clusterNamespace, Name: clusterName}, &cluster); err != nil {
		return nil, err
	}
	endpoint := server.ServiceName(cluster.Name) + "." + cluster.Namespace
	var b *bootstrap.ControlRuntimeBootstrap
	if err := retry.OnError(controller.Backoff, func(err error) bool {
		return err != nil
	}, func() error {
		var err error
		b, err = bootstrap.DecodedBootstrap(cluster.Spec.Token, endpoint)
		return err
	}); err != nil {
		return nil, fmt.Errorf("unable to decode bootstrap: %w", err)
	}
	adminCert, adminKey, err := kubeconfig.CreateClientCertKey(
		controller.AdminCommonName, []string{user.SystemPrivilegedGroup},
		nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, time.Hour*24*time.Duration(356),
		b.ClientCA.Content,
		b.ClientCAKey.Content)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s:%d", server.ServiceName(cluster.Name), server.ServerPort)
	kubeconfigData, err := kubeconfigBytes(url, []byte(b.ServerCA.Content), adminCert, adminKey)
	if err != nil {
		return nil, err
	}
	return clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
}

func kubeconfigBytes(url string, serverCA, clientCert, clientKey []byte) ([]byte, error) {
	config := clientcmdapi.NewConfig()

	cluster := clientcmdapi.NewCluster()
	cluster.CertificateAuthorityData = serverCA
	cluster.Server = url

	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.ClientCertificateData = clientCert
	authInfo.ClientKeyData = clientKey

	context := clientcmdapi.NewContext()
	context.AuthInfo = "default"
	context.Cluster = "default"

	config.Clusters["default"] = cluster
	config.AuthInfos["default"] = authInfo
	config.Contexts["default"] = context
	config.CurrentContext = "default"

	return clientcmd.Write(*config)
}

func loadTLSConfig(ctx context.Context, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace, nodeName, hostname string) (*tls.Config, error) {
	var (
		cluster v1alpha1.Cluster
		b       *bootstrap.ControlRuntimeBootstrap
	)
	if err := hostClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s.%s", server.ServiceName(cluster.Name), cluster.Namespace)
	if err := retry.OnError(controller.Backoff, func(err error) bool {
		return err != nil
	}, func() error {
		var err error
		b, err = bootstrap.DecodedBootstrap(cluster.Spec.Token, endpoint)
		return err
	}); err != nil {
		return nil, fmt.Errorf("unable to decode bootstrap: %w", err)
	}
	altNames := certutil.AltNames{
		DNSNames: []string{hostname},
	}
	cert, key, err := kubeconfig.CreateClientCertKey(nodeName, nil, &altNames, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, 0, b.ServerCA.Content, b.ServerCAKey.Content)
	if err != nil {
		return nil, fmt.Errorf("unable to get cert and key: %w", err)
	}
	clientCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("unable to get key pair: %w", err)
	}
	// create rootCA CertPool
	certs, err := certutil.ParseCertsPEM([]byte(b.ServerCA.Content))
	if err != nil {
		return nil, fmt.Errorf("unable to create ca certs: %w", err)
	}
	if len(certs) < 1 {
		return nil, fmt.Errorf("ca cert is not parsed correctly")
	}
	pool := x509.NewCertPool()
	pool.AddCert(certs[0])

	return &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{clientCert},
	}, nil
}

type LogWrapper struct {
	zap.SugaredLogger
}

func (l *LogWrapper) WithError(err error) log.Logger {
	return l
}

func (l *LogWrapper) WithField(string, interface{}) log.Logger {
	return l
}

func (l *LogWrapper) WithFields(field log.Fields) log.Logger {
	return l
}
