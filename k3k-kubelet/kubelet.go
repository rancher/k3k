package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/k3k-kubelet/provider"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	k3klog "github.com/rancher/k3k/pkg/log"
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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	ctrlserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	baseScheme     = runtime.NewScheme()
	k3kKubeletName = "k3k-kubelet"
)

func init() {
	_ = clientgoscheme.AddToScheme(baseScheme)
	_ = v1alpha1.AddToScheme(baseScheme)
}

type kubelet struct {
	name       string
	port       int
	hostConfig *rest.Config
	hostClient ctrlruntimeclient.Client
	virtClient kubernetes.Interface
	hostMgr    manager.Manager
	virtualMgr manager.Manager
	node       *nodeutil.Node
	logger     *k3klog.Logger
}

func newKubelet(ctx context.Context, c *config, logger *k3klog.Logger) (*kubelet, error) {
	hostConfig, err := clientcmd.BuildConfigFromFlags("", c.HostConfigPath)
	if err != nil {
		return nil, err
	}

	hostClient, err := ctrlruntimeclient.New(hostConfig, ctrlruntimeclient.Options{
		Scheme: baseScheme,
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

	hostMgr, err := ctrl.NewManager(hostConfig, manager.Options{
		Scheme: baseScheme,
		Metrics: ctrlserver.Options{
			BindAddress: ":8083",
		},
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				c.ClusterNamespace: {},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create controller-runtime mgr for host cluster: %s", err.Error())
	}

	virtualScheme := runtime.NewScheme()
	// virtual client will only use core types (for now), no need to add anything other than the basics
	err = clientgoscheme.AddToScheme(virtualScheme)
	if err != nil {
		return nil, fmt.Errorf("unable to add client go types to virtual cluster scheme: %s", err.Error())
	}
	virtualMgr, err := ctrl.NewManager(virtConfig, manager.Options{
		Scheme: virtualScheme,
		Metrics: ctrlserver.Options{
			BindAddress: ":8084",
		},
	})

	if err != nil {
		return nil, err
	}
	return &kubelet{
		name:       c.NodeName,
		hostConfig: hostConfig,
		hostClient: hostClient,
		hostMgr:    hostMgr,
		virtualMgr: virtualMgr,
		virtClient: virtClient,
		logger:     logger.Named(k3kKubeletName),
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
	// any one of the following 3 tasks (host manager, virtual manager, node) crashing will stop the
	// program, and all 3 of them block on start, so we start them here in go-routines
	go func() {
		err := k.hostMgr.Start(ctx)
		if err != nil {
			k.logger.Fatalw("host manager stopped", zap.Error(err))
		}
	}()

	go func() {
		err := k.virtualMgr.Start(ctx)
		if err != nil {
			k.logger.Fatalw("virtual manager stopped", zap.Error(err))
		}
	}()

	// run the node async so that we can wait for it to be ready in another call

	go func() {
		ctx = log.WithLogger(ctx, k.logger)
		if err := k.node.Run(ctx); err != nil {
			k.logger.Fatalw("node errored when running", zap.Error(err))
		}
	}()

	if err := k.node.WaitReady(context.Background(), time.Minute*1); err != nil {
		k.logger.Fatalw("node was not ready within timeout of 1 minute", zap.Error(err))
	}
	<-k.node.Done()
	if err := k.node.Err(); err != nil {
		k.logger.Fatalw("node stopped with an error", zap.Error(err))
	}
	k.logger.Info("node exited successfully")
}

func (k *kubelet) newProviderFunc(namespace, name, hostname string) nodeutil.NewProviderFunc {
	return func(pc nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		utilProvider, err := provider.New(*k.hostConfig, k.hostMgr, k.virtualMgr, k.logger, namespace, name)
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
