package cmds

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_validateCreateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     CreateConfig
		wantErr string
	}{
		{
			name: "valid full config",
			cfg: CreateConfig{
				servers:            1,
				persistenceType:    "dynamic",
				storageRequestSize: "10Gi",
				mode:               "shared",
			},
		},
		{
			name: "empty storage size",
			cfg: CreateConfig{
				servers:            1,
				storageRequestSize: "",
			},
		},
		{
			name: "zero servers",
			cfg: CreateConfig{
				servers: 0,
			},
			wantErr: "invalid number of servers",
		},
		{
			name: "negative servers",
			cfg: CreateConfig{
				servers: -1,
			},
			wantErr: "invalid number of servers",
		},
		{
			name: "empty persistence type",
			cfg: CreateConfig{
				servers:            1,
				persistenceType:    "",
				storageRequestSize: "10Gi",
			},
		},
		{
			name: "invalid persistence type",
			cfg: CreateConfig{
				servers:         1,
				persistenceType: "foo",
			},
			wantErr: `persistence-type should be one of "dynamic" or "ephemeral"`,
		},
		{
			name: "invalid storage size",
			cfg: CreateConfig{
				servers:            1,
				storageRequestSize: "abc",
			},
			wantErr: `invalid storage size, should be a valid resource quantity e.g "10Gi"`,
		},
		{
			name: "empty mode",
			cfg: CreateConfig{
				servers:            1,
				mode:               "",
				storageRequestSize: "10Gi",
			},
		},
		{
			name: "invalid mode",
			cfg: CreateConfig{
				servers:            1,
				mode:               "foo",
				storageRequestSize: "10Gi",
			},
			wantErr: `mode should be one of "shared", "virtual" or "hcp"`,
		},
		{
			name: "valid hcp mode",
			cfg: CreateConfig{
				servers:            1,
				mode:               "hcp",
				storageRequestSize: "10Gi",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCreateConfig(&tt.cfg)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
