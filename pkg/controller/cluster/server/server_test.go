package server

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// The server readiness probe must check /readyz (which includes the
// apiserver's etcd health checkers), not a bare TCP socket: the 6443
// listener accepts connections while raft has no quorum, so a TCP probe
// reports Ready for servers whose etcd is already dead and ordered
// StatefulSet rolls proceed into a quorum-less cluster. k3s runs the
// apiserver with anonymous-auth=false, so the check must exec kubectl
// (the k3s multicall binary does not dispatch "k3s kubectl" correctly in
// the server image) instead of probing over HTTPS, which would get a 401.
func TestPodSpecReadinessProbeChecksReadyz(t *testing.T) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mycluster",
			Namespace: "ns",
		},
	}

	s := New(cluster, nil, "token", "rancher/k3s:v1.33.5-k3s1", "IfNotPresent", nil)
	podSpec := s.podSpec(context.Background(), "rancher/k3s:v1.33.5-k3s1", "k3k-mycluster-server", false, "")

	if len(podSpec.Containers) == 0 {
		t.Fatal("podSpec has no containers")
	}

	probe := podSpec.Containers[0].ReadinessProbe
	if probe == nil {
		t.Fatal("server container has no readiness probe")
	}

	if probe.TCPSocket != nil {
		t.Fatal("readiness probe is a bare TCP check — blind to etcd quorum loss")
	}

	if probe.Exec == nil {
		t.Fatalf("expected an exec readiness probe, got: %+v", probe.ProbeHandler)
	}

	cmd := strings.Join(probe.Exec.Command, " ")
	if !strings.Contains(cmd, "kubectl get --raw=/readyz") {
		t.Fatalf("readiness probe does not check /readyz via kubectl: %q", cmd)
	}

	if strings.Contains(cmd, "k3s kubectl") {
		t.Fatalf("probe must exec kubectl directly, not the k3s multicall: %q", cmd)
	}
}
