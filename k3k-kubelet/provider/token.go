package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k3kcontroller "github.com/rancher/k3k/pkg/controller"
)

const (
	kubeAPIAccessPrefix          = "kube-api-access"
	serviceAccountTokenMountPath = "/var/run/secrets/kubernetes.io/serviceaccount"
)

// transformTokens copies the serviceaccount tokens used by virtualPod's serviceaccount to a secret on the host cluster and mount it
// to look like the serviceaccount token
func (p *Provider) transformTokens(ctx context.Context, virtualPod, hostPod *corev1.Pod) error {
	logger := p.logger.WithValues("namespace", virtualPod.Namespace, "name", virtualPod.Name, "serviceAccountName", virtualPod.Spec.ServiceAccountName)
	logger.V(1).Info("Transforming service account tokens")

	// transform projected service account token
	if err := p.transformProjectedTokens(ctx, virtualPod, hostPod); err != nil {
		return err
	}

	// transform kube-api-access token for all containers in virtualPod
	if err := p.transformKubeAccessToken(ctx, virtualPod, hostPod); err != nil {
		return err
	}

	return nil
}

func (p *Provider) transformKubeAccessToken(ctx context.Context, virtualPod, hostPod *corev1.Pod) error {
	// skip this process if the kube-api-access is already removed from the pod
	// this is needed in case users already adds their own custom tokens like in rancher imported clusters
	if !hasKubeAccessVolumeFound(virtualPod) {
		return nil
	}

	virtualSecretName := k3kcontroller.SafeConcatNameWithPrefix(virtualPod.Spec.ServiceAccountName, "token")

	virtualSecret := virtualSecret(virtualSecretName, virtualPod.Namespace, virtualPod.Spec.ServiceAccountName)
	if err := p.Virtual.Client.Create(ctx, virtualSecret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	// extracting the tokens data from the secret we just created
	virtualSecretKey := types.NamespacedName{
		Name:      virtualSecret.Name,
		Namespace: virtualSecret.Namespace,
	}
	if err := p.Virtual.Client.Get(ctx, virtualSecretKey, virtualSecret); err != nil {
		return err
	}
	// To avoid race conditions we need to check if the secret's data has been populated
	// including the token, ca.crt and namespace
	if len(virtualSecret.Data) < 3 {
		return fmt.Errorf("token secret %s/%s data is empty", virtualSecret.Namespace, virtualSecret.Name)
	}

	hostSecret, err := p.translateAndCreateHostTokenSecret(ctx, virtualSecret)
	if err != nil {
		return err
	}

	hostPod.Spec.ServiceAccountName = ""
	hostPod.Spec.DeprecatedServiceAccount = ""
	hostPod.Spec.AutomountServiceAccountToken = ptr.To(false)

	removeKubeAccessVolume(hostPod)
	addKubeAccessVolume(hostPod, hostSecret.Name)

	return nil
}

// transformProjectedTokens will iterate over the host pod projected volume sources
// and transform projected tokens to use a requested token secret from the virtual cluster
// instead the automatically generated secret on the host cluster.
func (p *Provider) transformProjectedTokens(ctx context.Context, virtualPod, hostPod *corev1.Pod) error {
	for i, volume := range hostPod.Spec.Volumes {
		if strings.HasPrefix(volume.Name, kubeAPIAccessPrefix) {
			continue
		}

		if volume.Projected == nil {
			continue
		}

		for j, source := range volume.Projected.Sources {
			if source.ServiceAccountToken == nil {
				continue
			}

			projectedSecret, err := p.requestTokenSecret(ctx, source.ServiceAccountToken, virtualPod)
			if err != nil {
				return err
			}

			hostSecret, err := p.translateAndCreateHostTokenSecret(ctx, projectedSecret)
			if err != nil {
				return err
			}
			// replace the projected token volume with a projected secret
			hostPod.Spec.Volumes[i].Projected.Sources[j].ServiceAccountToken = nil
			hostPod.Spec.Volumes[i].Projected.Sources[j].Secret = &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: hostSecret.Name,
				},
			}
		}
	}

	return nil
}

func (p *Provider) requestTokenSecret(ctx context.Context, token *corev1.ServiceAccountTokenProjection, virtualPod *corev1.Pod) (*corev1.Secret, error) {
	namespace := virtualPod.Namespace
	serviceAccountName := virtualPod.Spec.ServiceAccountName

	var audiences []string
	if token.Audience != "" {
		audiences = []string{token.Audience}
	}

	tokenRequest := &authv1.TokenRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: namespace,
		},
		Spec: authv1.TokenRequestSpec{
			Audiences:         audiences,
			ExpirationSeconds: token.ExpirationSeconds,
			BoundObjectRef: &authv1.BoundObjectReference{
				Name:       virtualPod.Name,
				UID:        virtualPod.UID,
				Kind:       "Pod",
				APIVersion: "v1",
			},
		},
	}

	tokenResp, err := p.Virtual.CoreClient.ServiceAccounts(namespace).CreateToken(ctx, serviceAccountName, tokenRequest, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	// create a virtual secret with that token
	virtualSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			// creating unique name for the virtual secret based on the request attributes
			Name:      generateTokenSecretName(serviceAccountName, token.Path, tokenResp),
			Namespace: namespace,
		},
		Data: map[string][]byte{
			token.Path: []byte(tokenResp.Status.Token),
		},
	}

	return virtualSecret, nil
}

func virtualSecret(name, namespace, serviceAccountName string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: serviceAccountName,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
}

func (p *Provider) translateAndCreateHostTokenSecret(ctx context.Context, projectedToken *corev1.Secret) (*corev1.Secret, error) {
	hostSecret := projectedToken.DeepCopy()
	hostSecret.Type = ""
	hostSecret.Annotations = make(map[string]string)

	p.Translator.TranslateTo(hostSecret)

	data := hostSecret.Data
	if _, err := controllerutil.CreateOrUpdate(ctx, p.Host.Client, hostSecret, func() error {
		hostSecret.Data = data
		return nil
	}); err != nil {
		return nil, err
	}

	return hostSecret, nil
}

func hasKubeAccessVolumeFound(pod *corev1.Pod) bool {
	for _, volume := range pod.Spec.Volumes {
		if strings.HasPrefix(volume.Name, kubeAPIAccessPrefix) {
			return true
		}
	}

	return false
}

func removeKubeAccessVolume(pod *corev1.Pod) {
	for i, volume := range pod.Spec.Volumes {
		if strings.HasPrefix(volume.Name, kubeAPIAccessPrefix) {
			pod.Spec.Volumes = append(pod.Spec.Volumes[:i], pod.Spec.Volumes[i+1:]...)
			break
		}
	}
	// init containers
	for i, container := range pod.Spec.InitContainers {
		for j, mountPath := range container.VolumeMounts {
			if strings.HasPrefix(mountPath.Name, kubeAPIAccessPrefix) {
				pod.Spec.InitContainers[i].VolumeMounts = append(pod.Spec.InitContainers[i].VolumeMounts[:j], pod.Spec.InitContainers[i].VolumeMounts[j+1:]...)
				break
			}
		}
	}

	// ephemeral containers
	for i, container := range pod.Spec.EphemeralContainers {
		for j, mountPath := range container.VolumeMounts {
			if strings.HasPrefix(mountPath.Name, kubeAPIAccessPrefix) {
				pod.Spec.EphemeralContainers[i].VolumeMounts = append(pod.Spec.EphemeralContainers[i].VolumeMounts[:j], pod.Spec.EphemeralContainers[i].VolumeMounts[j+1:]...)
				break
			}
		}
	}

	for i, container := range pod.Spec.Containers {
		for j, mountPath := range container.VolumeMounts {
			if strings.HasPrefix(mountPath.Name, kubeAPIAccessPrefix) {
				pod.Spec.Containers[i].VolumeMounts = append(pod.Spec.Containers[i].VolumeMounts[:j], pod.Spec.Containers[i].VolumeMounts[j+1:]...)
				break
			}
		}
	}
}

func addKubeAccessVolume(pod *corev1.Pod, hostSecretName string) {
	tokenVolumeName := k3kcontroller.SafeConcatNameWithPrefix(kubeAPIAccessPrefix)

	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: tokenVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: hostSecretName,
			},
		},
	})

	for i := range pod.Spec.InitContainers {
		pod.Spec.InitContainers[i].VolumeMounts = append(pod.Spec.InitContainers[i].VolumeMounts, corev1.VolumeMount{
			Name:      tokenVolumeName,
			MountPath: serviceAccountTokenMountPath,
		})
	}

	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].VolumeMounts = append(pod.Spec.Containers[i].VolumeMounts, corev1.VolumeMount{
			Name:      tokenVolumeName,
			MountPath: serviceAccountTokenMountPath,
		})
	}

	for i := range pod.Spec.EphemeralContainers {
		pod.Spec.EphemeralContainers[i].VolumeMounts = append(pod.Spec.EphemeralContainers[i].VolumeMounts, corev1.VolumeMount{
			Name:      tokenVolumeName,
			MountPath: serviceAccountTokenMountPath,
		})
	}
}

func generateTokenSecretName(serviceAccountName, tokenPath string, tokenReq *authv1.TokenRequest) string {
	nameComponents := []string{serviceAccountName}

	if tokenReq.Spec.Audiences != nil {
		nameComponents = append(nameComponents, tokenReq.Spec.Audiences...)
	}

	if tokenReq.Spec.ExpirationSeconds != nil {
		nameComponents = append(nameComponents, strconv.Itoa(int(*tokenReq.Spec.ExpirationSeconds)))
	}

	if tokenPath != "" {
		nameComponents = append(nameComponents, tokenPath)
	}

	return k3kcontroller.SafeConcatNameWithPrefix(nameComponents...)
}
