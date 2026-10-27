package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k3kcontroller "github.com/rancher/k3k/pkg/controller"
)

const (
	kubeAPIAccessPrefix          = "kube-api-access"
	serviceAccountTokenMountPath = "/var/run/secrets/kubernetes.io/serviceaccount"
	kubeRootCAConfigMapName      = "kube-root-ca.crt"

	// kubeAccessTokenExpirationSeconds is the expiration requested by the kubelet for the kube-api-access token.
	// The API server extends it to a year if --service-account-extend-token-expiration is enabled (the default).
	kubeAccessTokenExpirationSeconds = 3607
)

// transformTokens requests bound serviceaccount tokens for the virtualPod from the virtual cluster, stores them in
// secrets on the host cluster and mounts them in the hostPod in place of the serviceaccount token volumes.
// It returns the names of the host secrets holding the tokens.
func (p *Provider) transformTokens(ctx context.Context, virtualPod, hostPod *corev1.Pod) ([]string, error) {
	logger := p.logger.WithValues("namespace", virtualPod.Namespace, "name", virtualPod.Name, "serviceAccountName", virtualPod.Spec.ServiceAccountName)
	logger.V(1).Info("Transforming service account tokens")

	hostPod.Spec.ServiceAccountName = ""
	hostPod.Spec.DeprecatedServiceAccount = ""
	hostPod.Spec.AutomountServiceAccountToken = new(false)

	// transform projected service account token
	hostSecretNames, err := p.transformProjectedTokens(ctx, virtualPod, hostPod)
	if err != nil {
		return nil, err
	}

	// transform kube-api-access token for all containers in virtualPod
	hostSecretName, err := p.transformKubeAccessToken(ctx, virtualPod, hostPod)
	if err != nil {
		return nil, err
	}

	if hostSecretName != "" {
		hostSecretNames = append(hostSecretNames, hostSecretName)
	}

	return hostSecretNames, nil
}

func (p *Provider) transformKubeAccessToken(ctx context.Context, virtualPod, hostPod *corev1.Pod) (string, error) {
	// skip this process if the kube-api-access is already removed from the pod
	// this is needed in case users already adds their own custom tokens like in rancher imported clusters
	if !hasKubeAccessVolume(virtualPod) {
		return "", nil
	}

	virtualSecret, err := p.kubeAccessTokenSecret(ctx, virtualPod)
	if err != nil {
		return "", err
	}

	hostSecret, err := p.translateAndCreateHostTokenSecret(ctx, virtualSecret)
	if err != nil {
		return "", err
	}

	removeKubeAccessVolume(hostPod)
	addKubeAccessVolume(hostPod, hostSecret.Name)

	return hostSecret.Name, nil
}

// kubeAccessTokenSecret returns a secret with the same content as the kube-api-access projected volume
// that is injected in the pods: a token bound to the virtualPod, the virtual cluster CA and the namespace.
func (p *Provider) kubeAccessTokenSecret(ctx context.Context, virtualPod *corev1.Pod) (*corev1.Secret, error) {
	var rootCA corev1.ConfigMap

	rootCAKey := types.NamespacedName{
		Name:      kubeRootCAConfigMapName,
		Namespace: virtualPod.Namespace,
	}
	if err := p.virtual.Client.Get(ctx, rootCAKey, &rootCA); err != nil {
		return nil, err
	}

	caCert, found := rootCA.Data[corev1.ServiceAccountRootCAKey]
	if !found {
		return nil, fmt.Errorf("configmap %s/%s has no %s", rootCA.Namespace, rootCA.Name, corev1.ServiceAccountRootCAKey)
	}

	// same expiration requested by the kubelet for the kube-api-access token
	tokenResp, err := p.requestToken(ctx, virtualPod, "", new(int64(kubeAccessTokenExpirationSeconds)))
	if err != nil {
		return nil, err
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeAccessTokenSecretName(virtualPod.Name),
			Namespace: virtualPod.Namespace,
		},
		Data: map[string][]byte{
			corev1.ServiceAccountTokenKey:     []byte(tokenResp.Status.Token),
			corev1.ServiceAccountRootCAKey:    []byte(caCert),
			corev1.ServiceAccountNamespaceKey: []byte(virtualPod.Namespace),
		},
	}, nil
}

// transformProjectedTokens will iterate over the host pod projected volume sources
// and transform projected tokens to use a requested token secret from the virtual cluster
// instead the automatically generated secret on the host cluster.
func (p *Provider) transformProjectedTokens(ctx context.Context, virtualPod, hostPod *corev1.Pod) ([]string, error) {
	var hostSecretNames []string

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

			projectedSecret, err := p.projectedTokenSecret(ctx, source.ServiceAccountToken, virtualPod)
			if err != nil {
				return nil, err
			}

			hostSecret, err := p.translateAndCreateHostTokenSecret(ctx, projectedSecret)
			if err != nil {
				return nil, err
			}
			// replace the projected token volume with a projected secret
			hostPod.Spec.Volumes[i].Projected.Sources[j].ServiceAccountToken = nil
			hostPod.Spec.Volumes[i].Projected.Sources[j].Secret = &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: hostSecret.Name,
				},
			}

			hostSecretNames = append(hostSecretNames, hostSecret.Name)
		}
	}

	return hostSecretNames, nil
}

// projectedTokenSecret returns a secret holding a token bound to the virtualPod, requested with the
// attributes of the projected token.
func (p *Provider) projectedTokenSecret(ctx context.Context, token *corev1.ServiceAccountTokenProjection, virtualPod *corev1.Pod) (*corev1.Secret, error) {
	tokenResp, err := p.requestToken(ctx, virtualPod, token.Audience, token.ExpirationSeconds)
	if err != nil {
		return nil, err
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateTokenSecretName(virtualPod.Name, token),
			Namespace: virtualPod.Namespace,
		},
		Data: map[string][]byte{
			token.Path: []byte(tokenResp.Status.Token),
		},
	}, nil
}

// requestToken requests a token for the serviceaccount of the virtualPod, bound to the virtualPod.
func (p *Provider) requestToken(ctx context.Context, virtualPod *corev1.Pod, audience string, expirationSeconds *int64) (*authv1.TokenRequest, error) {
	namespace := virtualPod.Namespace
	serviceAccountName := virtualPod.Spec.ServiceAccountName

	var audiences []string
	if audience != "" {
		audiences = []string{audience}
	}

	tokenRequest := &authv1.TokenRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: namespace,
		},
		Spec: authv1.TokenRequestSpec{
			Audiences:         audiences,
			ExpirationSeconds: expirationSeconds,
			BoundObjectRef: &authv1.BoundObjectReference{
				Name:       virtualPod.Name,
				UID:        virtualPod.UID,
				Kind:       "Pod",
				APIVersion: "v1",
			},
		},
	}

	return p.virtual.CoreClient.ServiceAccounts(namespace).CreateToken(ctx, serviceAccountName, tokenRequest, metav1.CreateOptions{})
}

func (p *Provider) translateAndCreateHostTokenSecret(ctx context.Context, projectedToken *corev1.Secret) (*corev1.Secret, error) {
	hostSecret := projectedToken.DeepCopy()
	hostSecret.Type = ""
	hostSecret.Annotations = make(map[string]string)

	p.translator.TranslateTo(hostSecret)

	data := hostSecret.Data

	_, err := controllerutil.CreateOrUpdate(ctx, p.host.Client, hostSecret, func() error {
		hostSecret.Data = data
		return nil
	})
	if err != nil {
		return nil, err
	}

	return hostSecret, nil
}

// setTokenSecretsOwner sets the hostPod as owner of the host secrets holding its tokens,
// so they are garbage collected with the pod.
func (p *Provider) setTokenSecretsOwner(ctx context.Context, hostPod *corev1.Pod, hostSecretNames []string) error {
	var errs []error

	for _, name := range hostSecretNames {
		var hostSecret corev1.Secret

		key := types.NamespacedName{
			Name:      name,
			Namespace: hostPod.Namespace,
		}
		if err := p.host.Client.Get(ctx, key, &hostSecret); err != nil {
			errs = append(errs, err)
			continue
		}

		patch := client.MergeFrom(hostSecret.DeepCopy())

		if err := controllerutil.SetOwnerReference(hostPod, &hostSecret, p.host.Client.Scheme()); err != nil {
			errs = append(errs, err)
			continue
		}

		if err := p.host.Client.Patch(ctx, &hostSecret, patch); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func hasKubeAccessVolume(pod *corev1.Pod) bool {
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

func kubeAccessTokenSecretName(podName string) string {
	return k3kcontroller.SafeConcatNameWithPrefix(podName, kubeAPIAccessPrefix)
}

func generateTokenSecretName(podName string, token *corev1.ServiceAccountTokenProjection) string {
	nameComponents := []string{podName}

	if token.Audience != "" {
		nameComponents = append(nameComponents, sanitizeNameComponent(token.Audience))
	}

	if exp := token.ExpirationSeconds; exp != nil {
		nameComponents = append(nameComponents, strconv.FormatInt(*exp, 10))
	}

	if token.Path != "" {
		nameComponents = append(nameComponents, sanitizeNameComponent(token.Path))
	}

	return k3kcontroller.SafeConcatNameWithPrefix(nameComponents...)
}

var invalidNameChars = regexp.MustCompile(`[^a-z0-9.-]+`)

// sanitizeNameComponent lowercases s and replaces any character that is invalid in a Kubernetes
// name with "-". If the result is identical to s it is returned as is, otherwise a 6 character
// sha256 digest of the original is appended to keep distinct inputs distinct, in the form
// <sanitized-string>-<digest>.
func sanitizeNameComponent(s string) string {
	sanitized := strings.Trim(invalidNameChars.ReplaceAllString(strings.ToLower(s), "-"), "-.")
	if sanitized == s {
		return sanitized
	}

	digest := sha256.Sum256([]byte(s))

	return sanitized + "-" + hex.EncodeToString(digest[:])[:6]
}
