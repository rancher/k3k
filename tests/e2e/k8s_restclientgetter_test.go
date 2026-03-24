package k3k_test

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	memory "k8s.io/client-go/discovery/cached"
)

type RESTClientGetter struct {
	clientconfig    clientcmd.ClientConfig
	restConfig      *rest.Config
	discoveryClient discovery.CachedDiscoveryInterface
}

func NewRESTClientGetter(kubeconfig []byte) (*RESTClientGetter, error) {
	clientconfig, err := clientcmd.NewClientConfigFromBytes([]byte(kubeconfig))
	if err != nil {
		return nil, err
	}

	restConfig, err := clientconfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	dc, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	return &RESTClientGetter{
		clientconfig:    clientconfig,
		restConfig:      restConfig,
		discoveryClient: memory.NewMemCacheClient(dc),
	}, nil
}

func (r *RESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return r.restConfig, nil
}

func (r *RESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return r.discoveryClient, nil
}

func (r *RESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	return restmapper.NewDeferredDiscoveryRESTMapper(r.discoveryClient), nil
}

func (r *RESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return r.clientconfig
}
