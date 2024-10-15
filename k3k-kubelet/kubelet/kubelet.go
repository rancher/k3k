package kubelet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/k3k-kubelet/config"
	"github.com/rancher/k3k/k3k-kubelet/provider"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	"github.com/rancher/k3k/pkg/controller/util"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	Scheme  = runtime.NewScheme()
	backoff = wait.Backoff{
		Steps:    5,
		Duration: 5 * time.Second,
		Factor:   2,
		Jitter:   0.1,
	}
)

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)
}

type Kubelet struct {
	Name       string
	ServerName string
	Port       int
	TLSConfig  *tls.Config
	HostConfig *rest.Config
	HostClient ctrlruntimeclient.Client
	VirtClient kubernetes.Interface
	Node       *nodeutil.Node
}

func New(c *config.Type) (*Kubelet, error) {
	hostConfig, err := clientcmd.BuildConfigFromFlags("", c.HostConfigPath)
	if err != nil {
		return nil, err
	}

	hostClient, err := ctrlruntimeclient.New(hostConfig, ctrlruntimeclient.Options{
		Scheme: Scheme,
	})
	if err != nil {
		return nil, err
	}

	virtConfig, err := virtRestConfig(context.Background(), c.VirtualConfigPath, hostClient, c.ClusterName, c.ClusterNamespace)
	if err != nil {
		return nil, err
	}

	virtClient, err := kubernetes.NewForConfig(virtConfig)
	if err != nil {
		return nil, err
	}
	return &Kubelet{
		Name:       c.NodeName,
		HostConfig: hostConfig,
		HostClient: hostClient,
		VirtClient: virtClient,
	}, nil
}

func (k *Kubelet) RegisterNode(srvPort, namespace, name, podIP string) error {
	providerFunc := k.newProviderFunc(namespace, name, podIP)
	nodeOpts := k.nodeOpts(srvPort, namespace, name, podIP)

	var err error
	k.Node, err = nodeutil.NewNode(k.Name, providerFunc, nodeutil.WithClient(k.VirtClient), nodeOpts)
	if err != nil {
		return fmt.Errorf("unable to start kubelet: %v", err)
	}
	return nil
}

func (k *Kubelet) Start(ctx context.Context) {
	go func() {
		ctx := context.Background()
		logger, err := zap.NewProduction()
		if err != nil {
			fmt.Printf("unable to create logger: %s", err.Error())
			os.Exit(-1)
		}
		wrapped := LogWrapper{
			*logger.Sugar(),
		}
		ctx = log.WithLogger(ctx, &wrapped)
		err = k.Node.Run(ctx)
		if err != nil {
			fmt.Printf("node errored when running: %s \n", err.Error())
			os.Exit(-1)
		}
	}()
	if err := k.Node.WaitReady(context.Background(), time.Minute*1); err != nil {
		fmt.Printf("node was not ready within timeout of 1 minute: %s \n", err.Error())
		os.Exit(-1)
	}
	<-k.Node.Done()
	if err := k.Node.Err(); err != nil {
		fmt.Printf("node stopped with an error: %s \n", err.Error())
		os.Exit(-1)
	}
	fmt.Printf("node exited without an error")
}

func (k *Kubelet) newProviderFunc(namespace, name, podIP string) nodeutil.NewProviderFunc {
	return func(pc nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		utilProvider, err := provider.New(*k.HostConfig, namespace, name)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to make nodeutil provider %w", err)
		}
		nodeProvider := provider.Node{}

		provider.ConfigureNode(pc.Node, podIP, k.Port)
		return utilProvider, &nodeProvider, nil
	}
}

func (k *Kubelet) nodeOpts(srvPort, namespace, name, podIP string) nodeutil.NodeOpt {
	return func(c *nodeutil.NodeConfig) error {
		c.HTTPListenAddr = fmt.Sprintf(":%s", srvPort)
		// set up the routes
		mux := http.NewServeMux()
		err := nodeutil.AttachProviderRoutes(mux)(c)
		if err != nil {
			return fmt.Errorf("unable to attach routes: %w", err)
		}
		c.Handler = mux

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		tlsConfig, err := loadTLSConfig(ctx, k.HostClient, name, namespace, k.Name, podIP)
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
	endpoint := fmt.Sprintf("%s.%s", util.ServerSvcName(&cluster), util.ClusterNamespace(&cluster))
	var b *bootstrap.ControlRuntimeBootstrap
	if err := retry.OnError(backoff, func(err error) bool {
		return err == nil
	}, func() error {
		var err error
		b, err = bootstrap.DecodedBootstrap(cluster.Spec.Token, endpoint)
		return err
	}); err != nil {
		return nil, fmt.Errorf("unable to decode bootstrap: %w", err)
	}
	adminCert, adminKey, err := kubeconfig.CreateClientCertKey(
		util.AdminCommonName, []string{user.SystemPrivilegedGroup},
		nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, time.Hour*24*time.Duration(356),
		b.ClientCA.Content,
		b.ClientCAKey.Content)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s:%d", util.ServerSvcName(&cluster), util.ServerPort)
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

	kubeconfig, err := clientcmd.Write(*config)
	if err != nil {
		return nil, err
	}

	return kubeconfig, nil
}

func loadTLSConfig(ctx context.Context, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace, nodeName, ipStr string) (*tls.Config, error) {
	var (
		cluster v1alpha1.Cluster
		b       *bootstrap.ControlRuntimeBootstrap
	)
	if err := hostClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, &cluster); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s.%s", util.ServerSvcName(&cluster), util.ClusterNamespace(&cluster))
	if err := retry.OnError(backoff, func(err error) bool {
		return err != nil
	}, func() error {
		var err error
		b, err = bootstrap.DecodedBootstrap(cluster.Spec.Token, endpoint)
		return err
	}); err != nil {
		return nil, fmt.Errorf("unable to decode bootstrap: %w", err)
	}
	altNames := certutil.AltNames{
		IPs: []net.IP{net.ParseIP(ipStr)},
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
		return nil, fmt.Errorf("unable to create certs: %w", err)
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
