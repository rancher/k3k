package cmds

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	logrustest "github.com/sirupsen/logrus/hooks/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_standalonePath(t *testing.T) {
	tests := []struct {
		name      string
		flags     kubeconfigOutFlags
		wantPath  string
		wantWarns bool
	}{
		{
			name:      "the legacy file of the working directory is deprecated",
			flags:     kubeconfigOutFlags{},
			wantPath:  "k3k-foo-foo-kubeconfig.yaml",
			wantWarns: true,
		},
		{
			name:     "--out takes an explicit path",
			flags:    kubeconfigOutFlags{out: "/tmp/foo.yaml"},
			wantPath: "/tmp/foo.yaml",
		},
		{
			name:     "--no-out writes no file",
			flags:    kubeconfigOutFlags{noOut: true},
			wantPath: "",
		},
		{
			name:     "the deprecated --config-name still works",
			flags:    kubeconfigOutFlags{configName: "foo.yaml"},
			wantPath: "foo.yaml",
		},
	}

	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "k3k-foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := logrustest.NewGlobal()
			defer hook.Reset()

			assert.Equal(t, tt.wantPath, tt.flags.standalonePath(cluster))

			var warned bool

			for _, entry := range hook.AllEntries() {
				if entry.Level == logrus.WarnLevel {
					warned = true
				}
			}

			assert.Equal(t, tt.wantWarns, warned)
		})
	}
}
