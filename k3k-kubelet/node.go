package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"k8s.io/client-go/util/retry"

	certutil "github.com/rancher/dynamiclistener/cert"

	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
)

func (k *kubelet) registerNode(agentIP, podIP string, cfg config) error {
	tlsConfig, err := loadTLSConfig(cfg, k.name, k.token, agentIP, podIP)
	if err != nil {
		return errors.New("unable to get tls config: " + err.Error())
	}

	mux := http.NewServeMux()

	node, err := nodeutil.NewNode(
		k.name,
		k.newProviderFunc(cfg),
		nodeutil.WithClient(k.virtClient),
		nodeutil.AttachProviderRoutes(mux),
		nodeOpt(mux, tlsConfig, cfg.KubeletPort),
	)
	if err != nil {
		return errors.New("unable to start kubelet: " + err.Error())
	}

	k.node = node

	return nil
}

func nodeOpt(mux *http.ServeMux, tlsConfig *tls.Config, port int) nodeutil.NodeOpt {
	return func(c *nodeutil.NodeConfig) error {
		c.Handler = mux
		c.TLSConfig = tlsConfig

		c.HTTPListenAddr = fmt.Sprintf(":%d", port)

		c.NodeSpec.Labels["kubernetes.io/role"] = "worker"
		c.NodeSpec.Labels["node-role.kubernetes.io/worker"] = "true"

		c.SkipDownwardAPIResolution = true

		return nil
	}
}

func loadTLSConfig(cfg config, nodeName, token, agentIP, podIP string) (*tls.Config, error) {
	var b *bootstrap.ControlRuntimeBootstrap

	endpoint := fmt.Sprintf("%s.%s", server.ServiceName(cfg.ClusterName), cfg.ClusterNamespace)

	if err := retry.OnError(controller.Backoff, func(err error) bool {
		return err != nil
	}, func() error {
		var err error

		b, err = bootstrap.DecodedBootstrap(token, endpoint)

		return err
	}); err != nil {
		return nil, errors.New("unable to decode bootstrap: " + err.Error())
	}

	altNames := certutil.AltNames{
		DNSNames: []string{cfg.AgentHostname},
		IPs: []net.IP{
			net.ParseIP(agentIP),
			net.ParseIP(podIP),
		},
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
