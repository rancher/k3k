package server

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	auditContainerName = "audit-log"
	auditLogVolume     = "audit-log"
	auditPolicyVolume  = "audit-policy"
	auditPolicyKey     = "audit-policy.yaml"
	auditPolicyDir     = "/opt/rancher/k3s/audit"
	auditLogDir        = "/var/log/k3s-audit"
	auditLogFile       = "audit.log"

	// auditPolicyHashAnnotation is set on the server pods with a hash of the audit policy.
	// The kube-apiserver reads the policy only at startup, so a change of the policy
	// needs to roll out the server pods.
	auditPolicyHashAnnotation = "k3k.io/audit-policy-hash"

	// DefaultAuditPolicy is the audit policy used when audit logging is enabled without a policy.
	// It logs the metadata of every request.
	DefaultAuditPolicy = `apiVersion: audit.k8s.io/v1
kind: Policy
omitStages:
  - RequestReceived
rules:
  - level: Metadata
`
)

// Audit returns the audit configuration in effect for the cluster: the one enforced by the
// policy if it exists, otherwise the one from the spec. It returns nil if audit logging is disabled.
func Audit(cluster *v1beta1.Cluster) *v1beta1.AuditConfig {
	audit := cluster.Spec.Audit
	if cluster.Status.Policy != nil && cluster.Status.Policy.Audit != nil {
		audit = cluster.Status.Policy.Audit
	}

	if audit == nil || !audit.Enabled {
		return nil
	}

	return audit
}

// auditPolicy returns the audit policy to use for the given configuration.
func auditPolicy(audit *v1beta1.AuditConfig) string {
	if audit.Policy == "" {
		return DefaultAuditPolicy
	}

	return audit.Policy
}

// auditRotationArgs returns the K3s server arguments rotating the audit log, to bound its size
// in the server pod, since it's streamed by the audit sidecar.
// They are added before the user's server args, which can override them.
func auditRotationArgs() []string {
	return []string{
		"--kube-apiserver-arg=audit-log-maxsize=100",
		"--kube-apiserver-arg=audit-log-maxbackup=1",
	}
}

// auditPathArgs returns the K3s server arguments setting the audit log and policy files, which the
// audit sidecar and the policy mount rely on. K3s passes repeated kube-apiserver args through in
// order and the last one wins, so they are added after the user's server args to always take precedence.
func auditPathArgs() []string {
	return []string{
		"--kube-apiserver-arg=audit-log-path=" + filepath.Join(auditLogDir, auditLogFile),
		"--kube-apiserver-arg=audit-policy-file=" + filepath.Join(auditPolicyDir, auditPolicyKey),
	}
}

// auditAnnotations returns the pod annotations holding the hash of the audit policy.
func auditAnnotations(audit *v1beta1.AuditConfig) map[string]string {
	hash := sha256.Sum256([]byte(auditPolicy(audit)))

	return map[string]string{
		auditPolicyHashAnnotation: hex.EncodeToString(hash[:]),
	}
}

// addAuditLog adds to the server pod the volumes holding the audit policy and the audit log,
// and the sidecar container streaming the audit log to its stdout, so that it's available
// outside of the server pod (i.e. with "kubectl logs" or the host cluster logging stack).
func (s *Server) addAuditLog(podSpec *corev1.PodSpec, image string) {
	podSpec.Volumes = append(podSpec.Volumes,
		corev1.Volume{
			Name: auditPolicyVolume,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: configSecretName(s.cluster.Name, false),
					Items: []corev1.KeyToPath{
						{
							Key:  auditPolicyKey,
							Path: auditPolicyKey,
						},
					},
				},
			},
		},
		corev1.Volume{
			Name: auditLogVolume,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	)

	podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts,
		corev1.VolumeMount{
			Name:      auditPolicyVolume,
			MountPath: auditPolicyDir,
			ReadOnly:  true,
		},
		corev1.VolumeMount{
			Name:      auditLogVolume,
			MountPath: auditLogDir,
		},
	)

	// The sidecar runs as the same user of the server container, to be able to read the
	// audit log, but it never needs to be privileged.
	securityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: new(false),
		ReadOnlyRootFilesystem:   new(true),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}

	if serverSecurityContext := podSpec.Containers[0].SecurityContext; serverSecurityContext != nil {
		securityContext.RunAsUser = serverSecurityContext.RunAsUser
		securityContext.RunAsGroup = serverSecurityContext.RunAsGroup
		securityContext.RunAsNonRoot = serverSecurityContext.RunAsNonRoot
		securityContext.SeccompProfile = serverSecurityContext.SeccompProfile
	}

	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:            auditContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullPolicy(s.imagePullPolicy),
		// -F keeps following the log across rotations, and waits for it to be created by the kube-apiserver.
		Command: []string{"tail", "-n", "+1", "-F", filepath.Join(auditLogDir, auditLogFile)},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      auditLogVolume,
				MountPath: auditLogDir,
				ReadOnly:  true,
			},
		},
		SecurityContext: securityContext,
	})
}
