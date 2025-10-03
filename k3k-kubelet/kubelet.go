package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/log/klogv2"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	certutil "github.com/rancher/dynamiclistener/cert"
	v1 "k8s.io/api/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/rancher/k3k/k3k-kubelet/controller/syncer"
	k3kwebhook "github.com/rancher/k3k/k3k-kubelet/controller/webhook"
	"github.com/rancher/k3k/k3k-kubelet/provider"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
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
	virtualCluster v1alpha1.Cluster

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
	logger     logr.Logger
	token      string
}

func newKubelet(ctx context.Context, c *config, logger logr.Logger) (*kubelet, error) {
	hostConfig, err := clientcmd.BuildConfigFromFlags("", c.HostKubeconfig)
	if err != nil {
		return nil, err
	}

	hostClient, err := ctrlruntimeclient.New(hostConfig, ctrlruntimeclient.Options{
		Scheme: baseScheme,
	})
	if err != nil {
		return nil, err
	}

	virtConfig, err := virtRestConfig(ctx, c.VirtKubeconfig, hostClient, c.ClusterName, c.ClusterNamespace, c.Token, logger)
	if err != nil {
		return nil, err
	}

	virtClient, err := kubernetes.NewForConfig(virtConfig)
	if err != nil {
		return nil, err
	}

	ctrl.SetLogger(logger)

	hostMetricsBindAddress := ":8083"
	virtualMetricsBindAddress := ":8084"

	if c.MirrorHostNodes {
		hostMetricsBindAddress = "0"
		virtualMetricsBindAddress = "0"
	}

	hostMgr, err := ctrl.NewManager(hostConfig, manager.Options{
		Scheme:                  baseScheme,
		LeaderElection:          true,
		LeaderElectionNamespace: c.ClusterNamespace,
		LeaderElectionID:        c.ClusterName,
		Metrics: ctrlserver.Options{
			BindAddress: hostMetricsBindAddress,
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

	// virtual client will only use core types (for now), no need to add anything other than the basics
	virtualScheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(virtualScheme); err != nil {
		return nil, errors.New("unable to add client go types to virtual cluster scheme: " + err.Error())
	}

	webhookServer := webhook.NewServer(webhook.Options{
		CertDir: "/opt/rancher/k3k-webhook",
		Port:    c.WebhookPort,
	})

	virtualMgr, err := ctrl.NewManager(virtConfig, manager.Options{
		Scheme:                  virtualScheme,
		WebhookServer:           webhookServer,
		LeaderElection:          true,
		LeaderElectionNamespace: "kube-system",
		LeaderElectionID:        c.ClusterName,
		Metrics: ctrlserver.Options{
			BindAddress: virtualMetricsBindAddress,
		},
	})
	if err != nil {
		return nil, errors.New("unable to create controller-runtime mgr for virtual cluster: " + err.Error())
	}

	logger.Info("adding pod mutator webhook")

	if err := k3kwebhook.AddPodMutatorWebhook(ctx, virtualMgr, hostClient, c.ClusterName, c.ClusterNamespace, c.ServiceName, logger, c.WebhookPort); err != nil {
		return nil, errors.New("unable to add pod mutator webhook for virtual cluster: " + err.Error())
	}

	if err := addControllers(ctx, hostMgr, virtualMgr, c, hostClient); err != nil {
		return nil, errors.New("failed to add controller: " + err.Error())
	}

	clusterIP, err := clusterIP(ctx, c.ServiceName, c.ClusterNamespace, hostClient)
	if err != nil {
		return nil, errors.New("failed to extract the clusterIP for the server service: " + err.Error())
	}

	// get the cluster's DNS IP to be injected to pods
	var dnsService v1.Service

	dnsName := controller.SafeConcatNameWithPrefix(c.ClusterName, "kube-dns")
	if err := hostClient.Get(ctx, types.NamespacedName{Name: dnsName, Namespace: c.ClusterNamespace}, &dnsService); err != nil {
		return nil, errors.New("failed to get the DNS service for the cluster: " + err.Error())
	}

	var virtualCluster v1alpha1.Cluster
	if err := hostClient.Get(ctx, types.NamespacedName{Name: c.ClusterName, Namespace: c.ClusterNamespace}, &virtualCluster); err != nil {
		return nil, errors.New("failed to get virtualCluster spec: " + err.Error())
	}

	return &kubelet{
		virtualCluster: virtualCluster,

		name:       c.AgentHostname,
		hostConfig: hostConfig,
		hostClient: hostClient,
		virtConfig: virtConfig,
		virtClient: virtClient,
		hostMgr:    hostMgr,
		virtualMgr: virtualMgr,
		agentIP:    clusterIP,
		logger:     logger,
		token:      c.Token,
		dnsIP:      dnsService.Spec.ClusterIP,
		port:       c.KubeletPort,
	}, nil
}

func clusterIP(ctx context.Context, serviceName, clusterNamespace string, hostClient ctrlruntimeclient.Client) (string, error) {
	var service v1.Service

	serviceKey := types.NamespacedName{
		Namespace: clusterNamespace,
		Name:      serviceName,
	}

	if err := hostClient.Get(ctx, serviceKey, &service); err != nil {
		return "", err
	}

	return service.Spec.ClusterIP, nil
}

func (k *kubelet) registerNode(agentIP string, cfg config) error {
	providerFunc := k.newProviderFunc(cfg)
	nodeOpts := k.nodeOpts(cfg.KubeletPort, cfg.ClusterNamespace, cfg.ClusterName, cfg.AgentHostname, agentIP)

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
			k.logger.Error(err, "host manager stopped")
		}
	}()

	go func() {
		err := k.virtualMgr.Start(ctx)
		if err != nil {
			k.logger.Error(err, "virtual manager stopped")
		}
	}()

	// run the node async so that we can wait for it to be ready in another call

	go func() {
		klog.SetLogger(k.logger)
		ctx = log.WithLogger(ctx, klogv2.New(nil))
		if err := k.node.Run(ctx); err != nil {
			k.logger.Error(err, "node errored when running")
		}
	}()

	if err := k.node.WaitReady(context.Background(), time.Minute*1); err != nil {
		k.logger.Error(err, "node was not ready within timeout of 1 minute")
	}

	<-k.node.Done()

	if err := k.node.Err(); err != nil {
		k.logger.Error(err, "node stopped with an error")
	}

	k.logger.Info("node exited successfully")
}

func (k *kubelet) newProviderFunc(cfg config) nodeutil.NewProviderFunc {
	return func(pc nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		utilProvider, err := provider.New(*k.hostConfig, k.hostMgr, k.virtualMgr, k.logger, cfg.ClusterNamespace, cfg.ClusterName, cfg.ServerIP, k.dnsIP)
		if err != nil {
			return nil, nil, errors.New("unable to make nodeutil provider: " + err.Error())
		}

		provider.ConfigureNode(k.logger, pc.Node, cfg.AgentHostname, k.port, k.agentIP, utilProvider.CoreClient, utilProvider.VirtualClient, k.virtualCluster, cfg.Version, cfg.MirrorHostNodes)

		return utilProvider, &provider.Node{}, nil
	}
}

func (k *kubelet) nodeOpts(srvPort int, namespace, name, hostname, agentIP string) nodeutil.NodeOpt {
	return func(c *nodeutil.NodeConfig) error {
		c.HTTPListenAddr = fmt.Sprintf(":%d", srvPort)
		// set up the routes
		mux := http.NewServeMux()
		if err := nodeutil.AttachProviderRoutes(mux)(c); err != nil {
			return errors.New("unable to attach routes: " + err.Error())
		}

		c.Handler = mux

		tlsConfig, err := loadTLSConfig(name, namespace, k.name, hostname, k.token, agentIP)
		if err != nil {
			return errors.New("unable to get tls config: " + err.Error())
		}

		c.TLSConfig = tlsConfig

		return nil
	}
}

func virtRestConfig(ctx context.Context, virtualConfigPath string, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace, token string, logger logr.Logger) (*rest.Config, error) {
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
		logger.Error(err, "decoded bootstrap")
		return err
	}); err != nil {
		return nil, errors.New("unable to decode bootstrap: " + err.Error())
	}

	adminCert, adminKey, err := certs.CreateClientCertKey(
		controller.AdminCommonName,
		[]string{user.SystemPrivilegedGroup},
		nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		time.Hour*24*time.Duration(356),
		b.ClientCA.Content,
		b.ClientCAKey.Content,
	)
	if err != nil {
		return nil, err
	}

	url := "https://" + server.ServiceName(cluster.Name)

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

func loadTLSConfig(clusterName, clusterNamespace, nodeName, hostname, token, agentIP string) (*tls.Config, error) {
	var b *bootstrap.ControlRuntimeBootstrap

	endpoint := fmt.Sprintf("%s.%s", server.ServiceName(clusterName), clusterNamespace)

	if err := retry.OnError(controller.Backoff, func(err error) bool {
		return err != nil
	}, func() error {
		var err error
		b, err = bootstrap.DecodedBootstrap(token, endpoint)
		return err
	}); err != nil {
		return nil, errors.New("unable to decode bootstrap: " + err.Error())
	}
	// POD IP
	podIP := net.ParseIP(os.Getenv("POD_IP"))
	ip := net.ParseIP(agentIP)

	altNames := certutil.AltNames{
		DNSNames: []string{hostname},
		IPs:      []net.IP{ip, podIP},
	}

	cert, key, err := certs.CreateClientCertKey(nodeName, nil, &altNames, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, 0, b.ServerCA.Content, b.ServerCAKey.Content)
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

func addControllers(ctx context.Context, hostMgr, virtualMgr manager.Manager, c *config, hostClient ctrlruntimeclient.Client) error {
	var cluster v1alpha1.Cluster

	objKey := types.NamespacedName{
		Namespace: c.ClusterNamespace,
		Name:      c.ClusterName,
	}

	if err := hostClient.Get(ctx, objKey, &cluster); err != nil {
		return err
	}

	if err := syncer.AddConfigMapSyncer(ctx, virtualMgr, hostMgr, c.ClusterName, c.ClusterNamespace); err != nil {
		return errors.New("failed to add configmap global syncer: " + err.Error())
	}

	if err := syncer.AddSecretSyncer(ctx, virtualMgr, hostMgr, c.ClusterName, c.ClusterNamespace); err != nil {
		return errors.New("failed to add secret global syncer: " + err.Error())
	}

	logger.Info("adding service syncer controller")

	if err := syncer.AddServiceSyncer(ctx, virtualMgr, hostMgr, c.ClusterName, c.ClusterNamespace); err != nil {
		return errors.New("failed to add service syncer controller: " + err.Error())
	}

	logger.Info("adding ingress syncer controller")

	if err := syncer.AddIngressSyncer(ctx, virtualMgr, hostMgr, c.ClusterName, c.ClusterNamespace); err != nil {
		return errors.New("failed to add ingress syncer controller: " + err.Error())
	}

	logger.Info("adding pvc syncer controller")

	if err := syncer.AddPVCSyncer(ctx, virtualMgr, hostMgr, c.ClusterName, c.ClusterNamespace); err != nil {
		return errors.New("failed to add pvc syncer controller: " + err.Error())
	}

	logger.Info("adding pod pvc controller")

	if err := syncer.AddPodPVCController(ctx, virtualMgr, hostMgr, c.ClusterName, c.ClusterNamespace); err != nil {
		return errors.New("failed to add pod pvc controller: " + err.Error())
	}

	logger.Info("adding priorityclass controller")

	if err := syncer.AddPriorityClassSyncer(ctx, virtualMgr, hostMgr, c.ClusterName, c.ClusterNamespace); err != nil {
		return errors.New("failed to add priorityclass controller: " + err.Error())
	}

	return nil
}
