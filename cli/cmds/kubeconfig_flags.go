package cmds

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// kubeconfigOutFlags holds the flags controlling the standalone kubeconfig file written
// next to the context added to the user kubeconfig.
type kubeconfigOutFlags struct {
	out   string
	noOut bool

	// Deprecated: use out instead. Only registered on `kubeconfig generate`.
	configName string
}

// CobraFlagKubeconfigOut registers the flags controlling the standalone kubeconfig file.
func CobraFlagKubeconfigOut(cmd *cobra.Command, flags *kubeconfigOutFlags) {
	cmd.Flags().StringVar(&flags.out, "out", "", "also write a standalone kubeconfig of the cluster to this path")
	cmd.Flags().BoolVar(&flags.noOut, "no-out", false, "do not write a standalone kubeconfig file")

	cmd.MarkFlagsMutuallyExclusive("out", "no-out")

	if err := cmd.MarkFlagFilename("out"); err != nil {
		logrus.Fatal(err)
	}
}

// standalonePath returns the path of the standalone kubeconfig file to write for the
// cluster, or an empty string if no file should be written.
func (f *kubeconfigOutFlags) standalonePath(cluster *v1beta1.Cluster) string {
	switch {
	case f.noOut:
		return ""
	case f.out != "":
		return f.out
	case f.configName != "":
		// cobra already warned about the deprecation of the flag
		return f.configName
	}

	logrus.Warn("Writing a kubeconfig to the current directory is deprecated and will be removed in a future release. " +
		"The cluster is now added as a context to your kubeconfig. " +
		"Use --out to keep writing a standalone kubeconfig, or --no-out to silence this warning.")

	return cluster.Namespace + "-" + cluster.Name + "-kubeconfig.yaml"
}
