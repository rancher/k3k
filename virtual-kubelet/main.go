package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	"github.com/rancher/k3k/pkg/controller/util"
	"github.com/rancher/k3k/virtual-kubelet/pkg/provider"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	clusterNameEnv      = "CLUSTER_NAME"
	clusterNamespaceEnv = "CLUSTER_NAMESPACE"
	hostKubeconfigEnv   = "HOST_KUBECONFIG"
	virtKubeconfigEnv   = "VIRT_KUBECONFIG"
	podIPEnv            = "VIRT_POD_IP"
	srvPort             = 9443
	nodeName            = "virtual-node"
)

func main() {
	name, ok := os.LookupEnv(clusterNameEnv)
	if !ok {
		fmt.Printf("env var %s is required but was not provided \n", clusterNameEnv)
		os.Exit(-1)
	}
	namespace, ok := os.LookupEnv(clusterNamespaceEnv)
	if !ok {
		fmt.Printf("env var %s is required but was not provided \n", clusterNamespaceEnv)
		os.Exit(-1)
	}
	hostKubeconfigPath, ok := os.LookupEnv(hostKubeconfigEnv)
	if !ok {
		fmt.Printf("env var %s is required but was not provided \n", hostKubeconfigEnv)
		os.Exit(-1)
	}
	virtKubeconfigPath, ok := os.LookupEnv(virtKubeconfigEnv)
	if !ok {
		fmt.Printf("env var %s is required but was not provided \n", hostKubeconfigPath)
		os.Exit(-1)
	}
	podIP, ok := os.LookupEnv(podIPEnv)
	if !ok {
		fmt.Printf("env var %s is required but was not provided \n", podIPEnv)
		os.Exit(-1)
	}
	hostConfig, err := clientcmd.BuildConfigFromFlags("", hostKubeconfigPath)
	if err != nil {
		fmt.Printf("unable to load host kubeconfig at path %s, %s \n", hostKubeconfigPath, err)
		os.Exit(-1)
	}
	virtConfig, err := clientcmd.BuildConfigFromFlags("", virtKubeconfigPath)
	if err != nil {
		fmt.Printf("unable to load virtual kubeconfig at path %s, %s \n", virtKubeconfigPath, err)
		os.Exit(-1)
	}
	virtClientset, err := kubernetes.NewForConfig(virtConfig)
	if err != nil {
		fmt.Printf("unable to load virtual kubeconfig into kubernetes interface %s \n", err)
		os.Exit(-1)
	}
	node, err := nodeutil.NewNode("virtual-node", func(pc nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		utilProvider, err := provider.New(*hostConfig, namespace, name)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to make nodeutil provider %w", err)
		}
		nodeProvider := provider.Node{}
		provider.ConfigureNode(pc.Node, podIP, srvPort)
		return utilProvider, &nodeProvider, nil
	},
		nodeutil.WithClient(virtClientset),
		func(c *nodeutil.NodeConfig) error {
			c.HTTPListenAddr = fmt.Sprintf(":%d", srvPort)
			// set up the routes
			mux := http.NewServeMux()
			err := nodeutil.AttachProviderRoutes(mux)(c)
			if err != nil {
				return fmt.Errorf("unable to attach routes: %w", err)
			}
			c.Handler = mux

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()
			tlsConfig, err := loadTLSConfig(ctx, hostConfig, name, namespace, nodeName, podIP)
			if err != nil {
				return fmt.Errorf("unable to get tls config: %w", err)
			}
			c.TLSConfig = tlsConfig
			return nil
		},
	)
	if err != nil {
		fmt.Printf("unable to start kubelet: %s \n", err.Error())
		os.Exit(-1)
	}
	// run the node async so that we can wait for it to be ready in another call
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
		err = node.Run(ctx)
		if err != nil {
			fmt.Printf("node errored when running: %s \n", err.Error())
			os.Exit(-1)
		}
	}()
	if err := node.WaitReady(context.Background(), time.Minute*1); err != nil {
		fmt.Printf("node was not ready within timeout of 1 minute: %s \n", err.Error())
		os.Exit(-1)
	}
	<-node.Done()
	if err := node.Err(); err != nil {
		fmt.Printf("node stopped with an error: %s \n", err.Error())
		os.Exit(-1)
	}
	fmt.Printf("node exited without an error")
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

func loadTLSConfig(ctx context.Context, hostConfig *rest.Config, clusterName, clusterNamespace, nodeName, ipStr string) (*tls.Config, error) {
	dynamic, err := dynamic.NewForConfig(hostConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to get clientset for kubeconfig: %w", err)
	}
	clusterGVR := schema.GroupVersionResource{
		Group:    "k3k.io",
		Version:  "v1alpha1",
		Resource: "clusters",
	}
	dynCluster, err := dynamic.Resource(clusterGVR).Namespace(clusterNamespace).Get(ctx, clusterName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to get cluster: %w", err)
	}
	var cluster v1alpha1.Cluster
	bytes, err := json.Marshal(dynCluster)
	if err != nil {
		return nil, fmt.Errorf("unable to marshall cluster: %w", err)
	}
	err = json.Unmarshal(bytes, &cluster)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshall cluster: %w", err)
	}

	endpoint := fmt.Sprintf("%s.%s", util.ServerSvcName(&cluster), util.ClusterNamespace(&cluster))
	b, err := bootstrap.DecodedBootstrap(cluster.Spec.Token, endpoint)
	if err != nil {
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
