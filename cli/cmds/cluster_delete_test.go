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

func TestDeleteMissingCluster(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1beta1.AddToScheme(scheme))

	appCtx := &AppContext{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	err := delete(appCtx)(&cobra.Command{}, []string{"missing"})

	require.EqualError(t, err, `cluster "missing" not found in namespace "k3k-missing"`)
}

func Test_resolveClusterArg(t *testing.T) {
	tests := []struct {
		name          string
		flagNamespace string
		arg           string
		wantNamespace string
		wantName      string
		wantErr       bool
	}{
		{
			name:          "bare name defaults to k3k-<name>",
			arg:           "mycluster",
			wantNamespace: "k3k-mycluster",
			wantName:      "mycluster",
		},
		{
			name:          "bare name respects the namespace flag",
			flagNamespace: "custom",
			arg:           "mycluster",
			wantNamespace: "custom",
			wantName:      "mycluster",
		},
		{
			name:          "namespace/name form is split",
			arg:           "k3k-foo/mycluster",
			wantNamespace: "k3k-foo",
			wantName:      "mycluster",
		},
		{
			name:          "namespace/name matching the flag is accepted",
			flagNamespace: "k3k-foo",
			arg:           "k3k-foo/mycluster",
			wantNamespace: "k3k-foo",
			wantName:      "mycluster",
		},
		{
			name:          "namespace/name conflicting with the flag errors",
			flagNamespace: "bar",
			arg:           "k3k-foo/mycluster",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := &AppContext{namespace: tt.flagNamespace}

			namespace, name, err := resolveClusterArg(appCtx, tt.arg)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantNamespace, namespace)
			assert.Equal(t, tt.wantName, name)
		})
	}
}
