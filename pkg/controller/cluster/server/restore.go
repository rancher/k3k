package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"go.yaml.in/yaml/v4"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/k3s"
)

func (s *Server) restorePodSpec(ctx context.Context, image, name string, snapshot *v1beta1.EtcdSnapshot, serverIndex int) corev1.PodSpec {
	podSpec := s.baseRestorePodSpec(ctx, image, name, serverIndex)

	path := filepath.Join(k3sSnapshotDir, snapshot.Status.Filename)

	if snapshot.Spec.S3ConfigSecretRef != nil && serverIndex == 0 {
		podSpec.Volumes[0].Projected.Sources = append(podSpec.Volumes[0].Projected.Sources, buildS3ConfigVolume(name))
		path = snapshot.Status.Filename
	}

	cmd := restorePodCommand(path, serverIndex)

	podSpec.Containers[0].Command = cmd

	podSpec.Containers[0].Env = append(podSpec.Containers[0].Env, s.cluster.Spec.ServerEnvs...)

	for _, imagePullSecret := range s.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: imagePullSecret})
	}

	return podSpec
}

func (s *Server) baseRestorePodSpec(ctx context.Context, image, name string, serverIndex int) corev1.PodSpec {
	log := ctrl.LoggerFrom(ctx)

	serverAffinity := s.cluster.Spec.ServerAffinity
	if s.cluster.Status.Policy != nil && s.cluster.Status.Policy.ServerAffinity != nil {
		log.V(1).Info("Using server affinity from policy", "policyName", s.cluster.Status.PolicyName, "clusterName", s.cluster.Name)
		serverAffinity = s.cluster.Status.Policy.ServerAffinity
	}

	pvcName := fmt.Sprintf("varlibrancherk3s-%s-%d", controller.SafeConcatNameWithPrefix(s.cluster.Name, serverName), serverIndex)

	return corev1.PodSpec{
		Affinity:          serverAffinity,
		NodeSelector:      s.cluster.Spec.NodeSelector,
		PriorityClassName: s.cluster.Spec.PriorityClass,
		RestartPolicy:     corev1.RestartPolicyNever,
		Volumes: []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{
							{
								Secret: &corev1.SecretProjection{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configSecretName(s.cluster.Name, true),
									},
									Items: []corev1.KeyToPath{
										{
											Key:  "config.yaml",
											Path: "config.yaml",
										},
									},
								},
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
						Name:      "var-lib-rancher-k3s",
						MountPath: k3sDataDir,
						ReadOnly:  false,
					},
				},
			},
		},
	}
}

func (s *Server) ensureS3ConfigSecret(ctx context.Context, name string, snapshot *v1beta1.EtcdSnapshot, restoreObj *v1beta1.EtcdRestore) error {
	s3Config, err := k3s.GetS3ConfigFromSecret(ctx, s.client, snapshot)
	if err != nil {
		return err
	}

	s3ConfigData, err := yaml.Marshal(s3Config)
	if err != nil {
		return err
	}

	s3ConfigRestoreSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.cluster.Namespace,
		},
		Data: map[string][]byte{
			"s3.yaml": s3ConfigData,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, s.client, s3ConfigRestoreSecret, func() error {
		return controllerutil.SetControllerReference(restoreObj, s3ConfigRestoreSecret, s.client.Scheme())
	})

	return err
}

func buildS3ConfigVolume(secretName string) corev1.VolumeProjection {
	return corev1.VolumeProjection{
		Secret: &corev1.SecretProjection{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: secretName,
			},
			Items: []corev1.KeyToPath{
				{
					Key:  "s3.yaml",
					Path: "config.yaml.d/s3.yaml",
				},
			},
		},
	}
}

func restorePodCommand(snapshotPath string, serverIndex int) []string {
	if serverIndex > 0 {
		return []string{
			"/bin/sh",
			"-cx",
			"if [ -d /var/lib/rancher/k3s/server/db ]; then mv /var/lib/rancher/k3s/server/db /var/lib/rancher/k3s/server/db-old-$(date +%Y%m%d_%H%M%S); fi",
		}
	}

	return []string{
		"/bin/sh",
		"-c",
		"k3s server -c /opt/rancher/k3s/server/config.yaml --disable-agent --cluster-reset --cluster-reset-restore-path " + snapshotPath +
			" && touch /var/lib/rancher/k3s/server/db/restore-flag && rm /var/lib/rancher/k3s/server/db/reset-flag",
	}
}

// RestoreJobs returns a Job list object where it configures a job for the init server to run the restoration,
// and for the other servers to scrub the data dir before rejoining the cluster.
func (s *Server) RestoreJobs(ctx context.Context, restoreObj *v1beta1.EtcdRestore, snapshot *v1beta1.EtcdSnapshot, serverCount int) (*batchv1.JobList, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("EtcdRestore", restoreObj.Name)

	image := controller.K3SImage(s.cluster, s.image)

	var jobList batchv1.JobList

	for i := range serverCount {
		name := controller.SafeConcatNameWithPrefix(s.cluster.Name, "restore", restoreObj.Name, strconv.Itoa(i))

		job := batchv1.Job{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Job",
				APIVersion: "batch/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: s.cluster.Namespace,
			},
			Spec: batchv1.JobSpec{
				BackoffLimit: new(int32(3)),
			},
		}

		if snapshot.Spec.S3ConfigSecretRef != nil && i == 0 {
			log.V(1).Info("Using s3 config to pull snapshot", "S3ConfigSecretRef", snapshot.Spec.S3ConfigSecretRef, "clusterName", s.cluster.Name)

			if err := s.ensureS3ConfigSecret(ctx, name, snapshot, restoreObj); err != nil {
				return nil, err
			}
		}

		job.Spec.Template.Spec = s.restorePodSpec(ctx, image, name, snapshot, i)

		jobList.Items = append(jobList.Items, job)
	}

	return &jobList, nil
}
