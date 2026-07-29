package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
)

func (s *Server) restorePodSpec(ctx context.Context, image, name string, snapshot *v1beta1.EtcdSnapshot) corev1.PodSpec {
	log := ctrl.LoggerFrom(ctx)

	serverAffinity := s.cluster.Spec.ServerAffinity
	if s.cluster.Status.Policy != nil && s.cluster.Status.Policy.ServerAffinity != nil {
		log.V(1).Info("Using server affinity from policy", "policyName", s.cluster.Status.PolicyName, "clusterName", s.cluster.Name)
		serverAffinity = s.cluster.Status.Policy.ServerAffinity
	}

	// only restoring the snapshots on the first server's node
	pvcName := fmt.Sprintf("var-lib-rancher-k3s-%s", controller.SafeConcatNameWithPrefix(s.cluster.Name, "server-0"))
	podSpec := corev1.PodSpec{
		Affinity:          serverAffinity,
		NodeSelector:      s.cluster.Spec.NodeSelector,
		PriorityClassName: s.cluster.Spec.PriorityClass,
		RestartPolicy:     corev1.RestartPolicyNever,
		Volumes: []corev1.Volume{
			{
				Name: "init-config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: configSecretName(s.cluster.Name, true),
						Items: []corev1.KeyToPath{
							{
								Key:  "config.yaml",
								Path: "config.yaml",
							},
						},
					},
				},
			},
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: configSecretName(s.cluster.Name, false),
						Items: []corev1.KeyToPath{
							{
								Key:  "config.yaml",
								Path: "config.yaml",
							},
						},
					},
				},
			},
			{
				Name: "var-lib-rancher-k3s",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
					},
				},
			},
		},
		Containers: []corev1.Container{
			{
				Name:            name,
				Image:           image,
				ImagePullPolicy: corev1.PullPolicy(s.imagePullPolicy),
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "config",
						MountPath: k3sConfigDir,
						ReadOnly:  false,
					},
					{
						Name:      "init-config",
						MountPath: k3sInitConfigDir,
						ReadOnly:  false,
					},
					{
						Name:      "var-lib-rancher-k3s",
						MountPath: k3sDataDir,
						ReadOnly:  false,
					},
				},
			},
		},
	}

	snapshotAddress := strings.TrimPrefix(snapshot.Status.Filename, "file://")
	cmd := []string{
		"/bin/sh",
		"-c",
		"k3s server --disable-agent --cluster-reset --cluster-reset-restore-path " + snapshotAddress,
	}

	podSpec.Containers[0].Command = cmd

	podSpec.Containers[0].Env = append(podSpec.Containers[0].Env, s.cluster.Spec.ServerEnvs...)

	// add image pull secrets
	for _, imagePullSecret := range s.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: imagePullSecret})
	}

	return podSpec
}

func (s *Server) RestoreJob(ctx context.Context, restoreName string, snapshot *v1beta1.EtcdSnapshot) (*batchv1.Job, error) {
	image := controller.K3SImage(s.cluster, s.image)
	name := controller.SafeConcatNameWithPrefix(s.cluster.Name, "restore", restoreName)

	if s.cluster.Spec.Persistence.Type != v1beta1.DynamicPersistenceMode {
		return nil, errors.New("cluster restoration is only enabled with persistence mode")
	}

	var (
		volumes      []corev1.Volume
		volumeMounts []corev1.VolumeMount
	)

	// TODO: Think about airgap situation, secret mounts is a must here

	podSpec := s.restorePodSpec(ctx, image, name, snapshot)
	podSpec.Volumes = append(podSpec.Volumes, volumes...)
	podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, volumeMounts...)

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Job",
			APIVersion: "batch/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.cluster.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: podSpec,
			},
		},
	}, nil
}
