package provider

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// fakeManager stubs the one manager.Manager method ConfigureNode uses in the
// mirrorHostNodes path.
type fakeManager struct {
	manager.Manager
	reader client.Reader
}

func (f *fakeManager) GetAPIReader() client.Reader {
	return f.reader
}

func Test_ConfigureNode_mirrorHostNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	hostNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node-1",
			Labels:      map[string]string{"topology.kubernetes.io/zone": "01", "node-role.kubernetes.io/control-plane": ""},
			Annotations: map[string]string{"example.com/annotation": "value"},
			Finalizers:  []string{"example.com/finalizer"},
		},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{Key: "node.kubernetes.io/unschedulable", Effect: corev1.TaintEffectNoSchedule}},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("32"),
			},
			NodeInfo: corev1.NodeSystemInfo{
				KubeletVersion: "v1.32.13+rke2r1",
			},
			DaemonEndpoints: corev1.NodeDaemonEndpoints{
				KubeletEndpoint: corev1.DaemonEndpoint{Port: 10250},
			},
		},
	}

	hostMgr := &fakeManager{
		reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(hostNode).Build(),
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{},
		},
	}

	const (
		virtualVersion = "v1.34.9+k3s1"
		servicePort    = 50001
	)

	err = ConfigureNode(logr.Discard(), node, "node-1", servicePort, "10.0.0.1", hostMgr, nil, v1beta1.Cluster{}, virtualVersion, true)
	assert.NoError(t, err)

	// Mirrored from the host node.
	assert.Equal(t, hostNode.Labels, node.Labels)
	assert.Equal(t, hostNode.Annotations, node.Annotations)
	assert.Equal(t, hostNode.Finalizers, node.Finalizers)
	assert.Equal(t, hostNode.Spec.Taints, node.Spec.Taints)
	assert.Equal(t, hostNode.Status.Allocatable, node.Status.Allocatable)

	// Overridden after the mirror copy.
	assert.Equal(t, int32(servicePort), node.Status.DaemonEndpoints.KubeletEndpoint.Port)

	// The virtual cluster's version must win over the host's (regression
	// test for reporting the host version on mirrored nodes).
	assert.Equal(t, virtualVersion, node.Status.NodeInfo.KubeletVersion)
}

func Test_ConfigureNode_mirrorHostNodes_hostNodeMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	hostMgr := &fakeManager{
		reader: fake.NewClientBuilder().WithScheme(scheme).Build(),
	}

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}

	err = ConfigureNode(logr.Discard(), node, "node-1", 50001, "10.0.0.1", hostMgr, nil, v1beta1.Cluster{}, "v1.34.9+k3s1", true)
	assert.Error(t, err)
}
