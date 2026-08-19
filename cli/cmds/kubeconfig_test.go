package cmds

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// Test_generate_clusterKey checks how the cluster name and namespace are resolved from the
// NAME argument and the deprecated --name flag. The cluster is always missing, so the
// "not found" error reports the resolved key without going through the kubeconfig generation.
func Test_generate_clusterKey(t *testing.T) {
	tests := []struct {
		name          string
		flagNamespace string
		flagName      string
		args          []string
		wantErr       string
	}{
		{
			name:    "bare name argument defaults to k3k-<name>",
			args:    []string{"mycluster"},
			wantErr: `cluster "mycluster" not found in namespace "k3k-mycluster"`,
		},
		{
			name:    "namespace/name argument is split",
			args:    []string{"myns/mycluster"},
			wantErr: `cluster "mycluster" not found in namespace "myns"`,
		},
		{
			name:          "bare name argument respects the namespace flag",
			flagNamespace: "myns",
			args:          []string{"mycluster"},
			wantErr:       `cluster "mycluster" not found in namespace "myns"`,
		},
		{
			name:          "namespace/name argument conflicting with the namespace flag errors",
			flagNamespace: "otherns",
			args:          []string{"myns/mycluster"},
			wantErr:       `namespace mismatch: flag --namespace "otherns" conflicts with argument namespace "myns"`,
		},
		{
			name:     "deprecated name flag is still supported",
			flagName: "mycluster",
			wantErr:  `cluster "mycluster" not found in namespace "k3k-mycluster"`,
		},
		{
			name:          "deprecated name flag respects the namespace flag",
			flagNamespace: "myns",
			flagName:      "mycluster",
			wantErr:       `cluster "mycluster" not found in namespace "myns"`,
		},
		{
			name:     "the argument wins over the deprecated name flag",
			flagName: "ignored",
			args:     []string{"myns/mycluster"},
			wantErr:  `cluster "mycluster" not found in namespace "myns"`,
		},
		{
			name:    "no cluster name at all errors",
			wantErr: "expected exactly one cluster name",
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, v1beta1.AddToScheme(scheme))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := &AppContext{
				Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
				namespace: tt.flagNamespace,
			}
			cfg := &GenerateKubeconfigConfig{name: tt.flagName}

			err := generate(appCtx, cfg)(&cobra.Command{}, tt.args)

			assert.EqualError(t, err, tt.wantErr)
		})
	}
}
