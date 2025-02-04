package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

func Test_virtualAgentData(t *testing.T) {
	type args struct {
		serviceIP string
		token     string
	}
	tests := []struct {
		name         string
		args         args
		expectedData map[string]string
	}{
		{
			name: "simple config",
			args: args{
				serviceIP: "10.0.0.21",
				token:     "dnjklsdjnksd892389238",
			},
			expectedData: map[string]string{
				"server":       "https://10.0.0.21:6443",
				"token":        "dnjklsdjnksd892389238",
				"with-node-id": "true",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := virtualAgentData(tt.args.serviceIP, tt.args.token)

			data := make(map[string]string)
			err := yaml.Unmarshal([]byte(config), data)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedData, data)
		})
	}
}
