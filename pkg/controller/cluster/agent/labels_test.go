package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rancher/k3k/pkg/controller"
)

func TestAgentLabels(t *testing.T) {
	assert.Equal(t, map[string]string{
		"cluster": "test-cluster",
		"type":    "agent",
		"mode":    "shared",
	}, agentSelectorLabels("test-cluster", SharedNodeMode))

	assert.Equal(t, map[string]string{
		"cluster":                   "test-cluster",
		"type":                      "agent",
		"mode":                      "shared",
		controller.ClusterNameLabel: "test-cluster",
		"role":                      "agent",
	}, agentLabels("test-cluster", SharedNodeMode))
}
