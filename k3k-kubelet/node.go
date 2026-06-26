package main

import (
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"k8s.io/client-go/util/retry"

	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/k3s"
)

func (k *kubelet) registerNode(agentIP, podIP string, cfg config) error {
	tlsConfig, err := loadTLSConfig(cfg, k.token, agentIP, podIP)
	if err != nil {
		return fmt.Errorf("unable to get tls config: %w", err)
	}

	mux := http.NewServeMux()

	node, err := nodeutil.NewNode(
		k.name,
		k.newProviderFunc(cfg),
		nodeutil.WithClient(k.virtClient),
		nodeutil.AttachProviderRoutes(mux),
		nodeOpt(mux, tlsConfig, cfg.KubeletPort),
		func(c *nodeutil.NodeConfig) error {
			c.EventRecorder = k.virtEventRecorder
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("unable to start kubelet: %w", err)
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

	client := k3s.New(k3s.ClientConfig{
		ServerIP: serviceName,
		Token:    token,
		AgentIP:  agentIP,
		PodIP:    podIP,
		NodeName: controller.SafeConcatName(cfg.ClusterName, "server-0"),
	})

	var tlsCrt *tls.Certificate

	if err := retry.OnError(controller.Backoff, func(err error) bool {
		return err == k3s.ErrServerNotReady
	}, func() error {
		var err error

		tlsCrt, err = client.GetServingKubeletCrt()

		return err
	}); err != nil {
		return nil, fmt.Errorf("unable to request serving kubelet certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{*tlsCrt},
	}, nil
}
