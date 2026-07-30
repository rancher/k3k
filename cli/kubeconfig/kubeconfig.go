// Package kubeconfig manages the kubeconfig file k3kcli adds the virtual clusters to.
//
// This is the kubeconfig of the user, holding the credentials of every cluster they talk to,
// so the operations of this package are strictly additive: they only ever touch the entries of
// the virtual clusters they were asked about, and never the current context.
package kubeconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ContextName returns the name of the entries of a virtual cluster in a kubeconfig, in the
// "<namespace>/<name>" form used by the CLI arguments.
func ContextName(namespace, name string) string {
	return namespace + "/" + name
}

// ServerURL returns the server URL of the current context of config, or an empty string if
// there is none.
func ServerURL(config *clientcmdapi.Config) string {
	cluster, _, err := currentEntries(config)
	if err != nil {
		return ""
	}

	return cluster.Server
}

// Write writes config as a standalone kubeconfig file at path, creating the parent directory if
// needed, and returns the absolute path it wrote to.
func Write(config *clientcmdapi.Config, path string) (string, error) {
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		return "", err
	}

	return filepath.Abs(path)
}

// load loads the kubeconfig at path, returning an empty config if the file does not exist yet.
func load(path string) (*clientcmdapi.Config, error) {
	config, err := clientcmd.LoadFromFile(path)
	if os.IsNotExist(err) {
		return clientcmdapi.NewConfig(), nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load the kubeconfig '%s': %w", path, err)
	}

	return config, nil
}

// currentEntries returns the cluster and the credentials of the current context of config.
func currentEntries(config *clientcmdapi.Config) (*clientcmdapi.Cluster, *clientcmdapi.AuthInfo, error) {
	context, ok := config.Contexts[config.CurrentContext]
	if !ok {
		return nil, nil, fmt.Errorf("the kubeconfig has no '%s' context", config.CurrentContext)
	}

	cluster, ok := config.Clusters[context.Cluster]
	if !ok {
		return nil, nil, fmt.Errorf("the kubeconfig has no '%s' cluster", context.Cluster)
	}

	authInfo, ok := config.AuthInfos[context.AuthInfo]
	if !ok {
		return nil, nil, fmt.Errorf("the kubeconfig has no '%s' user", context.AuthInfo)
	}

	return cluster, authInfo, nil
}
