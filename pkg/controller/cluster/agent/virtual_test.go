package agent

import (
	"fmt"
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
		name string
		args args
		want string
	}{
		{
			name: "test",
			args: args{
				serviceIP: "10.0.0.21",
			},
			want: "server: https://%s:6443",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := virtualAgentData(tt.args.serviceIP, tt.args.token)

			data := make(map[string]string)
			err := yaml.Unmarshal([]byte(config), data)
			assert.NoError(t, err)
			fmt.Println(data)
		})
	}
}
