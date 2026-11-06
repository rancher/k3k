package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_Audit(t *testing.T) {
	specAudit := &v1beta1.AuditConfig{Enabled: true, Policy: "from-spec"}
	policyAudit := &v1beta1.AuditConfig{Enabled: true, Policy: "from-policy"}

	tests := map[string]struct {
		spec   *v1beta1.AuditConfig
		policy *v1beta1.AppliedPolicy
		want   *v1beta1.AuditConfig
	}{
		"no audit configuration": {},
		"audit disabled in the spec": {
			spec: &v1beta1.AuditConfig{Policy: "from-spec"},
		},
		"audit enabled in the spec": {
			spec: specAudit,
			want: specAudit,
		},
		"audit enabled by the policy": {
			policy: &v1beta1.AppliedPolicy{Audit: policyAudit},
			want:   policyAudit,
		},
		"audit from the policy takes precedence": {
			spec:   specAudit,
			policy: &v1beta1.AppliedPolicy{Audit: policyAudit},
			want:   policyAudit,
		},
		"audit disabled by the policy": {
			spec:   specAudit,
			policy: &v1beta1.AppliedPolicy{Audit: &v1beta1.AuditConfig{}},
		},
		"policy without an audit configuration falls back to the spec": {
			spec:   specAudit,
			policy: &v1beta1.AppliedPolicy{},
			want:   specAudit,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newTestCluster(func(c *v1beta1.Cluster) {
				c.Spec.Audit = tt.spec
				c.Status.Policy = tt.policy
			})

			assert.Equal(t, tt.want, Audit(cluster))
		})
	}
}

func Test_Config_AuditPolicy(t *testing.T) {
	service := &corev1.Service{Spec: corev1.ServiceSpec{ClusterIP: testServiceIP}}

	tests := map[string]struct {
		audit *v1beta1.AuditConfig
		want  string
	}{
		"audit disabled": {
			audit: &v1beta1.AuditConfig{Policy: "custom"},
		},
		"default policy": {
			audit: &v1beta1.AuditConfig{Enabled: true},
			want:  DefaultAuditPolicy,
		},
		"custom policy": {
			audit: &v1beta1.AuditConfig{Enabled: true, Policy: "custom"},
			want:  "custom",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newTestCluster(func(c *v1beta1.Cluster) {
				c.Spec.Audit = tt.audit
			})
			s := &Server{cluster: cluster}

			config, err := s.Config(false, service)
			require.NoError(t, err)

			policy, found := config.Data[auditPolicyKey]
			assert.Equal(t, tt.want != "", found)
			assert.Equal(t, tt.want, string(policy))

			initConfig, err := s.Config(true, service)
			require.NoError(t, err)
			assert.NotContains(t, initConfig.Data, auditPolicyKey, "the init config should not hold the audit policy")
		})
	}
}

func Test_StatefulServer_Audit(t *testing.T) {
	for _, mode := range []v1beta1.ClusterMode{v1beta1.SharedClusterMode, v1beta1.VirtualClusterMode, v1beta1.HCPClusterMode} {
		t.Run(string(mode), func(t *testing.T) {
			cluster := newTestCluster(func(c *v1beta1.Cluster) {
				c.Spec.Mode = mode
				c.Spec.Servers = new(int32(1))
				c.Spec.Persistence.Type = v1beta1.EphemeralPersistenceMode
				c.Spec.ServerArgs = []string{
					"--kube-apiserver-arg=audit-log-maxbackup=5",
					"--kube-apiserver-arg=audit-log-path=/tmp/audit.log",
				}
				c.Spec.Audit = &v1beta1.AuditConfig{Enabled: true}
			})
			s := New(cluster, nil, testToken, "rancher/k3s", "IfNotPresent", nil)

			ss, err := s.StatefulServer(context.Background(), nil)
			require.NoError(t, err)

			podSpec := ss.Spec.Template.Spec
			require.Len(t, podSpec.Containers, 2)

			server, sidecar := podSpec.Containers[0], podSpec.Containers[1]

			// the user's server args come after the rotation args, to be able to override them,
			// and before the log path and policy file, which always take precedence
			assert.Contains(t, server.Command[2], `EXTRA_ARGS="--kube-apiserver-arg=audit-log-maxsize=100 --kube-apiserver-arg=audit-log-maxbackup=1 `+
				`--kube-apiserver-arg=audit-log-maxbackup=5 --kube-apiserver-arg=audit-log-path=/tmp/audit.log `+
				`--kube-apiserver-arg=audit-log-path=/var/log/k3s-audit/audit.log `+
				`--kube-apiserver-arg=audit-policy-file=/opt/rancher/k3s/audit/audit-policy.yaml"`)
			assert.Contains(t, server.VolumeMounts, corev1.VolumeMount{Name: auditLogVolume, MountPath: auditLogDir})
			assert.Contains(t, server.VolumeMounts, corev1.VolumeMount{Name: auditPolicyVolume, MountPath: auditPolicyDir, ReadOnly: true})

			assert.Equal(t, auditContainerName, sidecar.Name)
			assert.Equal(t, server.Image, sidecar.Image)
			assert.Equal(t, []string{"tail", "-n", "+1", "-F", "/var/log/k3s-audit/audit.log"}, sidecar.Command)
			assert.Equal(t, []corev1.VolumeMount{{Name: auditLogVolume, MountPath: auditLogDir, ReadOnly: true}}, sidecar.VolumeMounts)
			assert.Nil(t, sidecar.SecurityContext.Privileged)

			assert.Equal(t, auditAnnotations(cluster.Spec.Audit), ss.Spec.Template.Annotations)
		})
	}
}

func Test_StatefulServer_AuditDisabled(t *testing.T) {
	cluster := newTestCluster(func(c *v1beta1.Cluster) {
		c.Spec.Mode = v1beta1.SharedClusterMode
		c.Spec.Servers = new(int32(1))
		c.Spec.Persistence.Type = v1beta1.EphemeralPersistenceMode
	})
	s := New(cluster, nil, testToken, "rancher/k3s", "IfNotPresent", nil)

	ss, err := s.StatefulServer(context.Background(), nil)
	require.NoError(t, err)

	assert.Len(t, ss.Spec.Template.Spec.Containers, 1)
	assert.NotContains(t, ss.Spec.Template.Spec.Containers[0].Command[2], "audit")
	assert.Empty(t, ss.Spec.Template.Annotations)
}

func Test_auditAnnotations(t *testing.T) {
	defaultPolicy := auditAnnotations(&v1beta1.AuditConfig{Enabled: true})

	assert.Equal(t, defaultPolicy, auditAnnotations(&v1beta1.AuditConfig{Enabled: true, Policy: DefaultAuditPolicy}))
	assert.NotEqual(t, defaultPolicy, auditAnnotations(&v1beta1.AuditConfig{Enabled: true, Policy: "custom"}),
		"a change of the policy should roll out the server pods")
}
