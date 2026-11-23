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

func TestUpdateClusterVersion(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		newVersion     string
		wantErr        string
	}{
		{
			name:           "upgrade rc to GA",
			currentVersion: "v1.37.1-rc2-k3s1",
			newVersion:     "v1.37.1-k3s1",
		},
		{
			name:           "upgrade rc to GA (1.35.10)",
			currentVersion: "v1.35.10-rc2-k3s1",
			newVersion:     "v1.35.10-k3s1",
		},
		{
			name:           "upgrade rc1 to rc2",
			currentVersion: "v1.37.1-rc1-k3s1",
			newVersion:     "v1.37.1-rc2-k3s1",
		},
		{
			name:           "downgrade rc2 to rc1",
			currentVersion: "v1.37.1-rc2-k3s1",
			newVersion:     "v1.37.1-rc1-k3s1",
			wantErr:        "downgrading cluster version is not supported",
		},
		{
			name:           "downgrade GA to rc",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "v1.37.1-rc2-k3s1",
			wantErr:        "downgrading cluster version is not supported",
		},
		{
			name:           "downgrade version",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "v1.36.5-k3s1",
			wantErr:        "downgrading cluster version is not supported",
		},
		{
			name:           "upgrade k3s build",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "v1.37.1-k3s2",
		},
		{
			name:           "downgrade k3s build",
			currentVersion: "v1.37.1-k3s2",
			newVersion:     "v1.37.1-k3s1",
			wantErr:        "downgrading cluster version is not supported",
		},
		{
			name:           "upgrade with plus build format",
			currentVersion: "v1.37.1-rc2+k3s1",
			newVersion:     "v1.37.1+k3s1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "k3k-foo"},
				Spec:       v1beta1.ClusterSpec{Version: tt.currentVersion},
			}
			cl := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()
			appCtx := &AppContext{Client: cl}

			config := &UpdateConfig{}
			cmd := &cobra.Command{}
			updateFlags(cmd, config)

			require.NoError(t, cmd.Flags().Set("version", tt.newVersion))
			require.NoError(t, cmd.Flags().Set("no-confirm", "true"))

			err := updateAction(appCtx, config)(cmd, []string{"foo"})
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			var updated v1beta1.Cluster
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, &updated))
			assert.Equal(t, tt.newVersion, updated.Spec.Version)
		})
	}
}

func Test_isDowngrade(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		newVersion     string
		wantDowngrade  bool
		wantErr        bool
	}{
		{
			name:           "upgrade rc to GA",
			currentVersion: "v1.37.1-rc2-k3s1",
			newVersion:     "v1.37.1-k3s1",
			wantDowngrade:  false,
		},
		{
			name:           "upgrade rc to GA without v prefix",
			currentVersion: "1.37.1-rc2-k3s1",
			newVersion:     "1.37.1-k3s1",
			wantDowngrade:  false,
		},
		{
			name:           "upgrade rc to GA (1.35.10)",
			currentVersion: "v1.35.10-rc2-k3s1",
			newVersion:     "v1.35.10-k3s1",
			wantDowngrade:  false,
		},
		{
			name:           "upgrade rc1 to rc2",
			currentVersion: "v1.37.1-rc1-k3s1",
			newVersion:     "v1.37.1-rc2-k3s1",
			wantDowngrade:  false,
		},
		{
			name:           "downgrade rc2 to rc1",
			currentVersion: "v1.37.1-rc2-k3s1",
			newVersion:     "v1.37.1-rc1-k3s1",
			wantDowngrade:  true,
		},
		{
			name:           "downgrade GA to rc",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "v1.37.1-rc2-k3s1",
			wantDowngrade:  true,
		},
		{
			name:           "upgrade patch version",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "v1.37.2-k3s1",
			wantDowngrade:  false,
		},
		{
			name:           "downgrade patch version",
			currentVersion: "v1.37.2-k3s1",
			newVersion:     "v1.37.1-k3s1",
			wantDowngrade:  true,
		},
		{
			name:           "upgrade minor version",
			currentVersion: "v1.36.5-k3s1",
			newVersion:     "v1.37.1-k3s1",
			wantDowngrade:  false,
		},
		{
			name:           "downgrade minor version",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "v1.36.5-k3s1",
			wantDowngrade:  true,
		},
		{
			name:           "upgrade k3s build suffix",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "v1.37.1-k3s2",
			wantDowngrade:  false,
		},
		{
			name:           "downgrade k3s build suffix",
			currentVersion: "v1.37.1-k3s2",
			newVersion:     "v1.37.1-k3s1",
			wantDowngrade:  true,
		},
		{
			name:           "upgrade k3s multi-digit build suffix",
			currentVersion: "v1.37.1-k3s2",
			newVersion:     "v1.37.1-k3s10",
			wantDowngrade:  false,
		},
		{
			name:           "downgrade k3s multi-digit build suffix",
			currentVersion: "v1.37.1-k3s10",
			newVersion:     "v1.37.1-k3s2",
			wantDowngrade:  true,
		},
		{
			name:           "upgrade with plus build format",
			currentVersion: "v1.37.1-rc2+k3s1",
			newVersion:     "v1.37.1+k3s1",
			wantDowngrade:  false,
		},
		{
			name:           "same version",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "v1.37.1-k3s1",
			wantDowngrade:  false,
		},
		{
			name:           "empty current suffix to k3s suffix",
			currentVersion: "v1.37.1",
			newVersion:     "v1.37.1-k3s1",
			wantDowngrade:  false,
		},
		{
			name:           "k3s suffix to empty suffix on same base",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "v1.37.1",
			wantDowngrade:  false,
		},
		{
			name:           "invalid current version",
			currentVersion: "invalid",
			newVersion:     "v1.37.1-k3s1",
			wantErr:        true,
		},
		{
			name:           "invalid new version",
			currentVersion: "v1.37.1-k3s1",
			newVersion:     "invalid",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isDowngrade(tt.currentVersion, tt.newVersion)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantDowngrade, got)
		})
	}
}

func Test_compareSuffix(t *testing.T) {
	assert.Equal(t, 0, compareSuffix("k3s1", "k3s1"))
	assert.Equal(t, -1, compareSuffix("k3s1", "k3s2"))
	assert.Equal(t, 1, compareSuffix("k3s2", "k3s1"))
	assert.Equal(t, -1, compareSuffix("k3s2", "k3s10"))
	assert.Equal(t, 1, compareSuffix("k3s10", "k3s2"))
	assert.Equal(t, 0, compareSuffix("same", "same"))
}
