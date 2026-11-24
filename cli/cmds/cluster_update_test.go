package cmds

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestUpdateCluster(t *testing.T) {
	tests := []struct {
		name          string
		flagNamespace string
		arg           string
		cluster       *v1beta1.Cluster
		wantErr       string
	}{
		{
			name:    "bare name defaults to k3k-<name>",
			arg:     "foo",
			cluster: &v1beta1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "k3k-foo"}},
		},
		{
			name:          "bare name respects the namespace flag",
			flagNamespace: "myns",
			arg:           "foo",
			cluster:       &v1beta1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"}},
		},
		{
			name:    "namespace/name form is split",
			arg:     "myns/foo",
			cluster: &v1beta1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"}},
		},
		{
			name: "servers can be set when only agents are defined",
			arg:  "myns/foo",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
				Spec:       v1beta1.ClusterSpec{Agents: new(int32(1))},
			},
		},
		{
			name:          "namespace/name conflicting with the flag errors",
			flagNamespace: "bar",
			arg:           "myns/foo",
			cluster:       &v1beta1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"}},
			wantErr:       `namespace mismatch: flag --namespace "bar" conflicts with argument namespace "myns"`,
		},
		{
			name:    "missing cluster",
			arg:     "missing",
			cluster: &v1beta1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "k3k-foo"}},
			wantErr: "cluster missing not found in namespace k3k-missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(tt.cluster).Build()
			appCtx := &AppContext{Client: cl, namespace: tt.flagNamespace}

			config := &UpdateConfig{}
			cmd := &cobra.Command{}
			updateFlags(cmd, config)

			require.NoError(t, cmd.Flags().Set("servers", "3"))
			require.NoError(t, cmd.Flags().Set("no-confirm", "true"))

			err := updateAction(appCtx, config)(cmd, []string{tt.arg})
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			var updated v1beta1.Cluster
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: tt.cluster.Name, Namespace: tt.cluster.Namespace}, &updated))

			require.NotNil(t, updated.Spec.Servers)
			assert.Equal(t, int32(3), *updated.Spec.Servers)
		})
	}
}

func TestUpdateClusterAgents(t *testing.T) {
	tests := []struct {
		name       string
		mode       v1beta1.ClusterMode
		agents     string
		wantAgents int32
		wantErr    string
	}{
		{
			name:       "virtual mode",
			mode:       v1beta1.VirtualClusterMode,
			agents:     "2",
			wantAgents: 2,
		},
		{
			name:       "hcp mode",
			mode:       v1beta1.HCPClusterMode,
			agents:     "2",
			wantAgents: 1,
			wantErr:    "invalid flag, --agents flag is only allowed in virtual mode",
		},
		{
			name:       "shared mode",
			mode:       v1beta1.SharedClusterMode,
			agents:     "2",
			wantAgents: 1,
			wantErr:    "invalid flag, --agents flag is only allowed in virtual mode",
		},
		{
			name:       "hcp mode can reset agents to 0",
			mode:       v1beta1.HCPClusterMode,
			agents:     "0",
			wantAgents: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
				Spec:       v1beta1.ClusterSpec{Mode: tt.mode, Agents: new(int32(1))},
			}

			cl := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()
			appCtx := &AppContext{Client: cl}

			config := &UpdateConfig{}
			cmd := &cobra.Command{}
			updateFlags(cmd, config)

			require.NoError(t, cmd.Flags().Set("agents", tt.agents))
			require.NoError(t, cmd.Flags().Set("no-confirm", "true"))

			err := updateAction(appCtx, config)(cmd, []string{"myns/foo"})
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			var updated v1beta1.Cluster
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: "foo", Namespace: "myns"}, &updated))

			require.NotNil(t, updated.Spec.Agents)
			assert.Equal(t, tt.wantAgents, *updated.Spec.Agents)
		})
	}
}
