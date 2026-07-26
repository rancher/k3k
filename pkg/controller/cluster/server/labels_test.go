package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rancher/k3k/pkg/controller"
)

func TestServerLabels(t *testing.T) {
	assert.Equal(t, map[string]string{
		"cluster": "test-cluster",
		"role":    "server",
	}, serverSelectorLabels("test-cluster"))

	assert.Equal(t, map[string]string{
		"cluster":                   "test-cluster",
		"role":                      "server",
		"mode":                      "virtual",
		controller.ClusterNameLabel: "test-cluster",
	}, serverLabels("test-cluster", "virtual"))
}
