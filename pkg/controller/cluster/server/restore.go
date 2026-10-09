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

func (s *Server) restorePodSpec(ctx context.Context, image, name string, snapshot *v1beta1.EtcdSnapshot, serverIndex int) (*corev1.PodSpec, error) {
	log := ctrl.LoggerFrom(ctx)

	serverAffinity := s.cluster.Spec.ServerAffinity
	if s.cluster.Status.Policy != nil && s.cluster.Status.Policy.ServerAffinity != nil {
		log.V(1).Info("Using server affinity from policy", "policyName", s.cluster.Status.PolicyName, "clusterName", s.cluster.Name)
		serverAffinity = s.cluster.Status.Policy.ServerAffinity
	}

	// only restoring the snapshots on the first server's node
	pvcName := fmt.Sprintf("varlibrancherk3s-%s-%d", controller.SafeConcatNameWithPrefix(s.cluster.Name, serverName), serverIndex)
	podSpec := &corev1.PodSpec{
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

	snapshotPath := filepath.Join(k3sSnapshotDir, snapshot.Status.Filename)
	etcdS3Flag := ""

	if snapshot.Spec.S3ConfigSecretRef != nil {
		log.V(1).Info("Using s3 config to pull snapshot", "S3ConfigSecretRef", snapshot.Spec.S3ConfigSecretRef, "clusterName", s.cluster.Name)
		snapshotPath = snapshot.Status.Filename

		s3Config, err := k3s.GetS3ConfigFromSecret(ctx, s.client, snapshot)
		if err != nil {
			return nil, err
		}

		// marshal the config to yaml to be used by k3s drop-in config
		s3ConfigData, err := yaml.Marshal(s3Config)
		if err != nil {
			return nil, err
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
			return controllerutil.SetControllerReference(s.cluster, s3ConfigRestoreSecret, s.client.Scheme())
		})
		if err != nil {
			return nil, err
		}

		// mounting the s3 secret to be picked by the k3s config
		podSpec.Volumes[0].Projected.Sources = append(podSpec.Volumes[0].Projected.Sources, corev1.VolumeProjection{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: s3ConfigRestoreSecret.Name,
				},
				Items: []corev1.KeyToPath{
					{
						Key:  "s3.yaml",
						Path: "config.yaml.d/s3.yaml",
					},
				},
			},
		})

		etcdS3Flag = " --etcd-s3"
	}

	// restoring will run as an agentless server and will drop a restore flag afterwards to make sure
	// the scaled up server will perform cluster reset since the job will run with different node IP.
	// The command also remove the reset flag since restoration process is also a cluster reset process.
	cmd := []string{
		"/bin/sh",
		"-c",
		"k3s server -c /opt/rancher/k3s/server/config.yaml --disable-agent" + etcdS3Flag + " --cluster-reset --cluster-reset-restore-path " + snapshotPath +
			" && touch /var/lib/rancher/k3s/server/db/restore-flag && rm /var/lib/rancher/k3s/server/db/reset-flag",
	}

	if serverIndex > 0 {
		// for other servers we simply scrub the data dir allowing correct join for HA servers
		// https://docs.k3s.io/cli/etcd-snapshot?etcdsnap=Multiple+Servers
		cmd = []string{
			"/bin/sh",
			"-cx",
			"if [ -d /var/lib/rancher/k3s/server/db ]; then mv /var/lib/rancher/k3s/server/db /var/lib/rancher/k3s/server/db-old-$(date +%Y%m%d_%H%M%S); fi",
		}
	}

	podSpec.Containers[0].Command = cmd

	podSpec.Containers[0].Env = append(podSpec.Containers[0].Env, s.cluster.Spec.ServerEnvs...)

	for _, imagePullSecret := range s.imagePullSecrets {
		podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: imagePullSecret})
	}

	return podSpec, nil
}

// RestoreJobs returns a Job list object where it configures a job for the init server to run the restoration,
// and for the other servers to scrub the data dir before rejoining the cluster.
func (s *Server) RestoreJobs(ctx context.Context, restoreName string, snapshot *v1beta1.EtcdSnapshot, serverCount int) (*batchv1.JobList, error) {
	image := controller.K3SImage(s.cluster, s.image)

	var jobList batchv1.JobList

	for i := range serverCount {
		name := controller.SafeConcatNameWithPrefix(s.cluster.Name, "restore", restoreName, strconv.Itoa(i))

		podSpec, err := s.restorePodSpec(ctx, image, name, snapshot, i)
		if err != nil {
			return nil, err
		}

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
				Template: corev1.PodTemplateSpec{
					Spec: *podSpec,
				},
			},
		}

		jobList.Items = append(jobList.Items, job)
	}

	return &jobList, nil
}
