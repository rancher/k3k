package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"

	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"k8s.io/client-go/util/retry"

	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/request"
)

func (k *kubelet) registerNode(agentIP, podIP string, cfg config) error {
	tlsConfig, err := loadTLSConfig(cfg, k.token, agentIP, podIP)
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

// loadTLSConfig function will request kubelet serving crt from k3s server and will use it to
// register a new node to the server, note that we use serving cert to allow adding IPSans to
// the certificate request
func loadTLSConfig(cfg config, token, agentIP, podIP string) (*tls.Config, error) {
	serviceName := fmt.Sprintf("%s.%s", server.ServiceName(cfg.ClusterName), cfg.ClusterNamespace)

	var (
		tlsCrtData []byte
		err        error
	)

	if err := retry.OnError(controller.Backoff, func(err error) bool {
		return err == request.ErrServerNotReady
	}, func() error {

		headers := map[string]string{
			"k3s-Node-Name":     controller.SafeConcatName(cfg.ClusterName, "server-0"),
			"k3s-Node-Password": token,
			"k3s-Node-IP":       agentIP + "," + podIP,
		}

		tlsCrtData, err = request.RequestK3sServer(serviceName, "/v1-k3s/client-kubelet.crt", "node", token, headers)
		return err
	}); err != nil {
		return nil, errors.New("unable to decode bootstrap: " + err.Error())
	}

	cert, key := certs.SplitCertKeyPEM(tlsCrtData)

	tlsCrt, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{tlsCrt},
	}, nil
}
