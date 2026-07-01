package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rancher/k3k/pkg/controller"
)

func Test_findNonLoopbackSAN(t *testing.T) {
	tests := []struct {
		name string
		sans []string
		want string
	}{
		{
			name: "loopback first, external second",
			sans: []string{"127.0.0.1", "10.0.0.100"},
			want: "10.0.0.100",
		},
		{
			name: "external first",
			sans: []string{"10.0.0.100", "127.0.0.1"},
			want: "10.0.0.100",
		},
		{
			name: "only loopback",
			sans: []string{"127.0.0.1", "::1"},
			want: "",
		},
		{
			name: "localhost hostname filtered",
			sans: []string{"localhost", "example.com"},
			want: "example.com",
		},
		{
			name: "external hostname",
			sans: []string{"hcp.example.com"},
			want: "hcp.example.com",
		},
		{
			name: "empty",
			sans: []string{},
			want: "",
		},
		{
			name: "ipv6 loopback filtered",
			sans: []string{"::1", "2001:db8::1"},
			want: "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findNonLoopbackSAN(tt.sans)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_hcpEndpointAddress(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantIP       string
		wantHostname string
		wantErr      bool
	}{
		{
			name:         "ipv4 literal is passed through",
			input:        "10.144.101.195",
			wantIP:       "10.144.101.195",
			wantHostname: "",
			wantErr:      false,
		},
		{
			name:    "unresolvable hostname errors",
			input:   "definitely-not-a-real-host.invalid",
			wantErr: true,
		},
		{
			name:    "ipv4 loopback literal is rejected",
			input:   "127.0.0.1",
			wantErr: true,
		},
		{
			name:    "ipv6 loopback literal is rejected",
			input:   "::1",
			wantErr: true,
		},
		{
			name:    "ipv4 loopback in range is rejected",
			input:   "127.0.0.100",
			wantErr: true,
		},
		{
			name:         "valid ipv6 literal passes through",
			input:        "2001:db8::1",
			wantIP:       "2001:db8::1",
			wantHostname: "",
			wantErr:      false,
		},
		{
			name:    "localhost hostname filters loopbacks",
			input:   "localhost",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hcpEndpointAddress(context.Background(), tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.Equal(t, tt.wantIP, got.IP)
			assert.Equal(t, tt.wantHostname, got.Hostname)
		})
	}
}

// Compile-time assertion: every reused exported name from the controller
// package below this test file must remain stable. If `controller.K3SImage`
// disappears (refactor), this guards the dependency.
var _ = controller.K3SImage
