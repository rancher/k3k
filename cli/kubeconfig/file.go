package kubeconfig

import (
	"k8s.io/client-go/tools/clientcmd"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// File is the kubeconfig file of the user.
type File struct {
	path string
}

// New returns the kubeconfig file at path.
//
// An empty path is resolved the way kubectl resolves the file it writes to: the first usable
// entry of $KUBECONFIG, then $HOME/.kube/config.
func New(path string) *File {
	pathOptions := clientcmd.NewDefaultPathOptions()

	if path != "" {
		pathOptions.LoadingRules.ExplicitPath = path
	}

	return &File{path: pathOptions.GetDefaultFilename()}
}

// Path returns the path of the file.
func (f *File) Path() string {
	return f.path
}

// Add adds the virtual cluster described by src to the file, keyed by name: the cluster and the
// credentials of the current context of src are copied over, and a context named name is
// created for them.
//
// The merge is strictly additive. Any other entry of the file, its preferences and its current
// context are left untouched, and src is not modified. An entry already keyed by name is
// overwritten, which is what makes it possible to re-generate a kubeconfig to rotate its
// certificates. The file and its parent directory are created if they don't exist.
func (f *File) Add(name string, src *clientcmdapi.Config) error {
	srcCluster, srcAuthInfo, err := currentEntries(src)
	if err != nil {
		return err
	}

	config, err := load(f.path)
	if err != nil {
		return err
	}

	context := clientcmdapi.NewContext()
	context.Cluster = name
	context.AuthInfo = name

	config.Clusters[name] = srcCluster.DeepCopy()
	config.AuthInfos[name] = srcAuthInfo.DeepCopy()
	config.Contexts[name] = context

	return clientcmd.WriteToFile(*config, f.path)
}

// Remove removes the cluster, user and context entries of the given names, returning the names
// that were found and removed.
//
// A missing file is not an error, and the file is not written at all - not even created - when
// none of the names matched. The current context is cleared if it referenced a removed context,
// to avoid leaving a dangling reference behind.
func (f *File) Remove(names ...string) ([]string, error) {
	config, err := load(f.path)
	if err != nil {
		return nil, err
	}

	var removed []string

	for _, name := range names {
		_, hasCluster := config.Clusters[name]
		_, hasAuthInfo := config.AuthInfos[name]
		_, hasContext := config.Contexts[name]

		if !hasCluster && !hasAuthInfo && !hasContext {
			continue
		}

		delete(config.Clusters, name)
		delete(config.AuthInfos, name)
		delete(config.Contexts, name)

		if config.CurrentContext == name {
			config.CurrentContext = ""
		}

		removed = append(removed, name)
	}

	if len(removed) == 0 {
		return nil, nil
	}

	return removed, clientcmd.WriteToFile(*config, f.path)
}
