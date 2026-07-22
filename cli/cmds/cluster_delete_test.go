package cmds

import (
	"testing"

	"github.com/spf13/cobra"
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
