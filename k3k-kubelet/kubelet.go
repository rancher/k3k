package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	k3kkubeletcontroller "github.com/rancher/k3k/k3k-kubelet/controller"
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
	v1 "k8s.io/api/core/v1"
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
	virtConfig *rest.Config
	agentIP    string
	dnsIP      string
	hostClient ctrlruntimeclient.Client
	virtClient kubernetes.Interface
	hostMgr    manager.Manager
	virtualMgr manager.Manager
	node       *nodeutil.Node
	logger     *k3klog.Logger
	token      string
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
	virtConfig, err := virtRestConfig(ctx, c.VirtualConfigPath, hostClient, c.ClusterName, c.ClusterNamespace, c.Token, logger)
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
		return nil, errors.New("unable to create controller-runtime mgr for host cluster: " + err.Error())
	}

	virtualScheme := runtime.NewScheme()
	// virtual client will only use core types (for now), no need to add anything other than the basics
	err = clientgoscheme.AddToScheme(virtualScheme)
	if err != nil {
		return nil, errors.New("unable to add client go types to virtual cluster scheme: " + err.Error())
	}
	virtualMgr, err := ctrl.NewManager(virtConfig, manager.Options{
		Scheme: virtualScheme,
		Metrics: ctrlserver.Options{
			BindAddress: ":8084",
		},
	})
	if err != nil {
		return nil, errors.New("unable to create controller-runtime mgr for virtual cluster: " + err.Error())
	}

	logger.Info("adding service syncer controller")
	if err := k3kkubeletcontroller.AddServiceSyncer(ctx, virtualMgr, hostMgr, c.ClusterName, c.ClusterNamespace, k3klog.New(false)); err != nil {
		return nil, errors.New("failed to add service syncer controller: " + err.Error())
	}

	clusterIP, err := clusterIP(ctx, c.AgentHostname, c.ClusterNamespace, hostClient)
	if err != nil {
		return nil, errors.New("failed to extract the clusterIP for the server service: " + err.Error())
	}

	// get the cluster's DNS IP to be injected to pods
	var dnsService v1.Service
	dnsName := controller.SafeConcatNameWithPrefix(c.ClusterName, "kube-dns")
	if err := hostClient.Get(ctx, types.NamespacedName{Name: dnsName, Namespace: c.ClusterNamespace}, &dnsService); err != nil {
		return nil, errors.New("failed to get the DNS service for the cluster: " + err.Error())
	}

	return &kubelet{
		name:       c.NodeName,
		hostConfig: hostConfig,
		hostClient: hostClient,
		virtConfig: virtConfig,
		virtClient: virtClient,
		hostMgr:    hostMgr,
		virtualMgr: virtualMgr,
		agentIP:    clusterIP,
		logger:     logger.Named(k3kKubeletName),
		token:      c.Token,
		dnsIP:      dnsService.Spec.ClusterIP,
	}, nil
}

func clusterIP(ctx context.Context, serviceName, clusterNamespace string, hostClient ctrlruntimeclient.Client) (string, error) {
	var service v1.Service
	serviceKey := types.NamespacedName{Namespace: clusterNamespace, Name: serviceName}
	if err := hostClient.Get(ctx, serviceKey, &service); err != nil {
		return "", err
	}
	return service.Spec.ClusterIP, nil
}

func (k *kubelet) registerNode(ctx context.Context, agentIP, srvPort, namespace, name, hostname, serverIP, dnsIP string) error {
	providerFunc := k.newProviderFunc(namespace, name, hostname, agentIP, serverIP, dnsIP)
	nodeOpts := k.nodeOpts(ctx, srvPort, namespace, name, hostname, agentIP)

	var err error
	k.node, err = nodeutil.NewNode(k.name, providerFunc, nodeutil.WithClient(k.virtClient), nodeOpts)
	if err != nil {
		return errors.New("unable to start kubelet: " + err.Error())
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

func (k *kubelet) newProviderFunc(namespace, name, hostname, agentIP, serverIP, dnsIP string) nodeutil.NewProviderFunc {
	return func(pc nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		utilProvider, err := provider.New(*k.hostConfig, k.hostMgr, k.virtualMgr, k.logger, namespace, name, serverIP, dnsIP)
		if err != nil {
			return nil, nil, errors.New("unable to make nodeutil provider: " + err.Error())
		}

		provider.ConfigureNode(pc.Node, hostname, k.port, agentIP)

		return utilProvider, &provider.Node{}, nil
	}
}

func (k *kubelet) nodeOpts(ctx context.Context, srvPort, namespace, name, hostname, agentIP string) nodeutil.NodeOpt {
	return func(c *nodeutil.NodeConfig) error {
		c.HTTPListenAddr = fmt.Sprintf(":%s", srvPort)
		// set up the routes
		mux := http.NewServeMux()
		if err := nodeutil.AttachProviderRoutes(mux)(c); err != nil {
			return errors.New("unable to attach routes: " + err.Error())
		}
		c.Handler = mux

		tlsConfig, err := loadTLSConfig(ctx, k.hostClient, name, namespace, k.name, hostname, k.token, agentIP)
		if err != nil {
			return errors.New("unable to get tls config: " + err.Error())
		}
		c.TLSConfig = tlsConfig
		return nil
	}
}

func virtRestConfig(ctx context.Context, virtualConfigPath string, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace, token string, logger *k3klog.Logger) (*rest.Config, error) {
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
		b, err = bootstrap.DecodedBootstrap(token, endpoint)
		logger.Infow("decoded bootstrap", zap.Error(err))
		return err
	}); err != nil {
		return nil, errors.New("unable to decode bootstrap: " + err.Error())
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

func loadTLSConfig(ctx context.Context, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace, nodeName, hostname, token, agentIP string) (*tls.Config, error) {
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
		b, err = bootstrap.DecodedBootstrap(token, endpoint)
		return err
	}); err != nil {
		return nil, errors.New("unable to decode bootstrap: " + err.Error())
	}
	ip := net.ParseIP(agentIP)
	altNames := certutil.AltNames{
		DNSNames: []string{hostname},
		IPs:      []net.IP{ip},
	}
	cert, key, err := kubeconfig.CreateClientCertKey(nodeName, nil, &altNames, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, 0, b.ServerCA.Content, b.ServerCAKey.Content)
	if err != nil {
		return nil, errors.New("unable to get cert and key: " + err.Error())
	}
	clientCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, errors.New("unable to get key pair: " + err.Error())
	}
	// create rootCA CertPool
	certs, err := certutil.ParseCertsPEM([]byte(b.ServerCA.Content))
	if err != nil {
		return nil, errors.New("unable to create ca certs: " + err.Error())
	}
	if len(certs) < 1 {
		return nil, errors.New("ca cert is not parsed correctly")
	}
	pool := x509.NewCertPool()
	pool.AddCert(certs[0])

	return &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{clientCert},
	}, nil
}
