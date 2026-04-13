package client

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	memory "k8s.io/client-go/discovery/cached"
)

// RESTClientGetter is a Kubernetes REST client getter implementation that satisfies
// the genericclioptions.RESTClientGetter interface. This is used primarily for Helm
// operations in tests.
type RESTClientGetter struct {
	clientconfig    clientcmd.ClientConfig
	restConfig      *rest.Config
	discoveryClient discovery.CachedDiscoveryInterface
}

// NewRESTClientGetter creates a new RESTClientGetter from kubeconfig bytes.
// This is used for Helm operations in tests.
func NewRESTClientGetter(kubeconfig []byte) (*RESTClientGetter, error) {
	clientconfig, err := clientcmd.NewClientConfigFromBytes(kubeconfig)
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

// ToRESTConfig returns the REST config.
func (r *RESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return r.restConfig, nil
}

// ToDiscoveryClient returns the cached discovery client.
func (r *RESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return r.discoveryClient, nil
}

// ToRESTMapper returns a REST mapper from the discovery client.
func (r *RESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	return restmapper.NewDeferredDiscoveryRESTMapper(r.discoveryClient), nil
}

// ToRawKubeConfigLoader returns the raw kubeconfig loader.
func (r *RESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return r.clientconfig
}
