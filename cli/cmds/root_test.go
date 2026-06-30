package cmds

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func Test_InitializeConfig_envPrefix(t *testing.T) {
	tests := []struct {
		name    string
		envName string
		want    string
	}{
		{
			name:    "unprefixed env does not bind to flag",
			envName: "VERSION",
			want:    "",
		},
		{
			name:    "prefixed env binds to flag",
			envName: "K3KCLI_VERSION",
			want:    "1h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Setenv(tt.envName, "1h")

			var version string
			cmd := &cobra.Command{Use: "create"}
			cmd.Flags().StringVar(&version, "version", "", "k3s version")

			InitializeConfig(cmd)

			assert.Equal(t, tt.want, version)
		})
	}
}
