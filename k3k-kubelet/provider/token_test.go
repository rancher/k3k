package provider

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/controller/certificates/rootcacertpublisher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clienttesting "k8s.io/client-go/testing"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
)

// newTokenTestProvider returns a Provider whose virtual cluster issues tokens in the form
// "<pod>/<audience>/<expirationSeconds>", recording the received token requests.
func newTokenTestProvider(t *testing.T, requests *[]*authv1.TokenRequest) *Provider {
	t.Helper()

	rootCA := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: rootcacertpublisher.RootCACertConfigMapName, Namespace: "default"},
		Data:       map[string]string{corev1.ServiceAccountRootCAKey: "virtual-ca"},
	}

	virtualClientset := fake.NewClientset()
	virtualClientset.PrependReactor("create", "serviceaccounts", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "token" {
			return false, nil, nil
		}

		req := action.(clienttesting.CreateAction).GetObject().(*authv1.TokenRequest).DeepCopy()
		*requests = append(*requests, req)

		var exp int64
		if req.Spec.ExpirationSeconds != nil {
			exp = *req.Spec.ExpirationSeconds
		}

		audience := ""
		if len(req.Spec.Audiences) > 0 {
			audience = req.Spec.Audiences[0]
		}

		req.Status.Token = fmt.Sprintf("%s/%s/%d", req.Spec.BoundObjectRef.Name, audience, exp)
		req.Status.ExpirationTimestamp = metav1.NewTime(time.Now().Add(time.Duration(exp) * time.Second))

		return true, req, nil
	})

	return &Provider{
		host: ClusterContext{
			Client: ctrlfake.NewClientBuilder().Build(),
		},
		virtual: ClusterContext{
			Client:     ctrlfake.NewClientBuilder().WithObjects(rootCA).Build(),
			CoreClient: virtualClientset.CoreV1(),
		},
		translator: translate.ToHostTranslator{
			ClusterName:      "c-test",
			ClusterNamespace: "ns-test",
		},
		logger: logr.Discard(),
	}
}

func newTokenTestPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name + "-uid"),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "my-serviceaccount",
			Containers: []corev1.Container{{
				Name: "main",
				VolumeMounts: []corev1.VolumeMount{
					{Name: "kube-api-access-abc", MountPath: serviceAccountTokenMountPath},
					{Name: "vault-token", MountPath: "/var/run/secrets/vault"},
				},
			}},
			Volumes: []corev1.Volume{
				{
					Name: "kube-api-access-abc",
					VolumeSource: corev1.VolumeSource{
						Projected: &corev1.ProjectedVolumeSource{
							Sources: []corev1.VolumeProjection{
								{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{ExpirationSeconds: new(int64(3607)), Path: "token"}},
							},
						},
					},
				},
				{
					Name: "vault-token",
					VolumeSource: corev1.VolumeSource{
						Projected: &corev1.ProjectedVolumeSource{
							Sources: []corev1.VolumeProjection{
								{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Audience: "vault", ExpirationSeconds: new(int64(600)), Path: "token"}},
							},
						},
					},
				},
			},
		},
	}
}

func Test_transformTokens(t *testing.T) {
	ctx := context.Background()

	var requests []*authv1.TokenRequest

	p := newTokenTestProvider(t, &requests)

	getHostSecret := func(t *testing.T, name string) *corev1.Secret {
		t.Helper()

		var secret corev1.Secret

		key := types.NamespacedName{Name: name, Namespace: "ns-test"}
		require.NoError(t, p.host.Client.Get(ctx, key, &secret))

		return &secret
	}

	transform := func(t *testing.T, virtualPod *corev1.Pod) (*corev1.Pod, []string) {
		t.Helper()

		hostPod := virtualPod.DeepCopy()
		hostSecretNames, err := p.transformTokens(ctx, virtualPod, hostPod)
		require.NoError(t, err)

		return hostPod, hostSecretNames
	}

	virtualPod := newTokenTestPod("pod-a")
	hostPod, hostSecretNames := transform(t, virtualPod)

	require.Len(t, hostSecretNames, 2)
	assert.Empty(t, hostPod.Spec.ServiceAccountName)
	assert.Equal(t, new(false), hostPod.Spec.AutomountServiceAccountToken)

	// all the tokens are bound to the virtual pod
	require.Len(t, requests, 2)

	for _, req := range requests {
		assert.Equal(t, "my-serviceaccount", req.Name)
		assert.Equal(t, &authv1.BoundObjectReference{Kind: "Pod", APIVersion: "v1", Name: "pod-a", UID: "pod-a-uid"}, req.Spec.BoundObjectRef)
	}

	t.Run("projected token is replaced by a secret", func(t *testing.T) {
		require.Len(t, hostPod.Spec.Volumes, 2)

		vaultVolume := hostPod.Spec.Volumes[0]
		assert.Equal(t, "vault-token", vaultVolume.Name)
		require.Len(t, vaultVolume.Projected.Sources, 1)
		assert.Nil(t, vaultVolume.Projected.Sources[0].ServiceAccountToken)
		require.NotNil(t, vaultVolume.Projected.Sources[0].Secret)

		secret := getHostSecret(t, vaultVolume.Projected.Sources[0].Secret.Name)
		assert.Equal(t, map[string][]byte{"token": []byte("pod-a/vault/600")}, secret.Data)
	})

	t.Run("kube-api-access is replaced by a secret with a bound token", func(t *testing.T) {
		kubeAccessVolume := hostPod.Spec.Volumes[1]
		assert.Equal(t, k3kcontroller.SafeConcatNameWithPrefix(kubeAPIAccessPrefix), kubeAccessVolume.Name)
		require.NotNil(t, kubeAccessVolume.Secret)

		secret := getHostSecret(t, kubeAccessVolume.Secret.SecretName)
		assert.Equal(t, map[string][]byte{
			"token":     []byte("pod-a//3607"),
			"ca.crt":    []byte("virtual-ca"),
			"namespace": []byte("default"),
		}, secret.Data)
	})

	t.Run("no legacy token secret is created in the virtual cluster", func(t *testing.T) {
		var secrets corev1.SecretList
		require.NoError(t, p.virtual.Client.List(ctx, &secrets))
		assert.Empty(t, secrets.Items)
	})

	t.Run("pods with the same serviceaccount get their own secrets", func(t *testing.T) {
		otherHostPod, otherHostSecretNames := transform(t, newTokenTestPod("pod-b"))

		require.Len(t, otherHostSecretNames, 2)
		assert.NotContains(t, hostSecretNames, otherHostSecretNames[0])
		assert.NotContains(t, hostSecretNames, otherHostSecretNames[1])

		secret := getHostSecret(t, otherHostPod.Spec.Volumes[1].Secret.SecretName)
		assert.Equal(t, []byte("pod-b//3607"), secret.Data["token"])

		// the tokens of the first pod are untouched
		secret = getHostSecret(t, hostPod.Spec.Volumes[1].Secret.SecretName)
		assert.Equal(t, []byte("pod-a//3607"), secret.Data["token"])
	})
}

func Test_transformTokens_missingRootCA(t *testing.T) {
	var requests []*authv1.TokenRequest

	p := newTokenTestProvider(t, &requests)

	virtualPod := newTokenTestPod("pod-a")
	virtualPod.Namespace = "no-root-ca"

	_, err := p.transformTokens(context.Background(), virtualPod, virtualPod.DeepCopy())
	assert.Error(t, err)
}

func Test_setTokenSecretsOwner(t *testing.T) {
	ctx := context.Background()

	hostPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "host-pod", Namespace: "ns-test", UID: "host-pod-uid"},
	}
	hostSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "host-secret", Namespace: "ns-test"},
	}

	// the secret is patched without being read, as it could be missing from the cache if it was just created
	hostClient := ctrlfake.NewClientBuilder().
		WithObjects(hostSecret).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Secret); ok {
					return apierrors.NewNotFound(corev1.Resource("secrets"), key.Name)
				}

				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	p := &Provider{
		host: ClusterContext{
			Client: hostClient,
		},
	}

	require.NoError(t, p.setTokenSecretsOwner(ctx, hostPod, []string{"host-secret"}))

	var secrets corev1.SecretList
	require.NoError(t, p.host.Client.List(ctx, &secrets))
	require.Len(t, secrets.Items, 1)

	hostSecret = &secrets.Items[0]
	require.Len(t, hostSecret.OwnerReferences, 1)
	assert.Equal(t, "Pod", hostSecret.OwnerReferences[0].Kind)
	assert.Equal(t, "host-pod", hostSecret.OwnerReferences[0].Name)
	assert.Equal(t, types.UID("host-pod-uid"), hostSecret.OwnerReferences[0].UID)

	assert.Error(t, p.setTokenSecretsOwner(ctx, hostPod, []string{"missing-secret"}))
}

func Test_hasKubeAccessVolume(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "no volumes",
			pod:  &corev1.Pod{},
			want: false,
		},
		{
			name: "volume with kube-api-access prefix",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "kube-api-access-abc123"},
					},
				},
			},
			want: true,
		},
		{
			name: "exact kube-api-access name",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "kube-api-access"},
					},
				},
			},
			want: true,
		},
		{
			name: "volume without kube-api-access prefix",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "my-volume"},
					},
				},
			},
			want: false,
		},
		{
			name: "multiple volumes with one kube-api-access",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "config-volume"},
						{Name: "kube-api-access-xyz"},
						{Name: "data-volume"},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasKubeAccessVolume(tt.pod))
		})
	}
}

func Test_removeKubeAccessVolume(t *testing.T) {
	t.Run("removes volume and all volume mounts from containers", func(t *testing.T) {
		pod := &corev1.Pod{
			Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{
					{Name: "config-volume"},
					{Name: "kube-api-access-abc"},
					{Name: "data-volume"},
				},
				InitContainers: []corev1.Container{
					{
						Name: "init",
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config-volume", MountPath: "/config"},
							{Name: "kube-api-access-abc", MountPath: serviceAccountTokenMountPath},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name: "main",
						VolumeMounts: []corev1.VolumeMount{
							{Name: "kube-api-access-abc", MountPath: serviceAccountTokenMountPath},
							{Name: "data-volume", MountPath: "/data"},
						},
					},
				},
				EphemeralContainers: []corev1.EphemeralContainer{
					{
						EphemeralContainerCommon: corev1.EphemeralContainerCommon{
							Name: "debug",
							VolumeMounts: []corev1.VolumeMount{
								{Name: "kube-api-access-abc", MountPath: serviceAccountTokenMountPath},
							},
						},
					},
				},
			},
		}

		removeKubeAccessVolume(pod)

		// Verify volume was removed
		assert.Equal(t, 2, len(pod.Spec.Volumes))
		assert.Equal(t, "config-volume", pod.Spec.Volumes[0].Name)
		assert.Equal(t, "data-volume", pod.Spec.Volumes[1].Name)

		// Verify init container mount was removed
		assert.Equal(t, 1, len(pod.Spec.InitContainers[0].VolumeMounts))
		assert.Equal(t, "config-volume", pod.Spec.InitContainers[0].VolumeMounts[0].Name)

		// Verify container mount was removed
		assert.Equal(t, 1, len(pod.Spec.Containers[0].VolumeMounts))
		assert.Equal(t, "data-volume", pod.Spec.Containers[0].VolumeMounts[0].Name)

		// Verify ephemeral container mount was removed
		assert.Equal(t, 0, len(pod.Spec.EphemeralContainers[0].VolumeMounts))
	})

	t.Run("no kube-api-access volume present", func(t *testing.T) {
		pod := &corev1.Pod{
			Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{
					{Name: "config-volume"},
				},
				Containers: []corev1.Container{
					{
						Name: "main",
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config-volume", MountPath: "/config"},
						},
					},
				},
			},
		}

		removeKubeAccessVolume(pod)

		assert.Equal(t, 1, len(pod.Spec.Volumes))
		assert.Equal(t, "config-volume", pod.Spec.Volumes[0].Name)
		assert.Equal(t, 1, len(pod.Spec.Containers[0].VolumeMounts))
	})
}

func Test_addKubeAccessVolume(t *testing.T) {
	tokenVolumeName := k3kcontroller.SafeConcatNameWithPrefix(kubeAPIAccessPrefix)
	hostSecretName := "host-secret-token"

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{Name: "existing-volume"},
			},
			InitContainers: []corev1.Container{
				{Name: "init"},
			},
			Containers: []corev1.Container{
				{Name: "main"},
				{Name: "sidecar"},
			},
			EphemeralContainers: []corev1.EphemeralContainer{
				{
					EphemeralContainerCommon: corev1.EphemeralContainerCommon{
						Name: "debug",
					},
				},
			},
		},
	}

	addKubeAccessVolume(pod, hostSecretName)

	// Verify volume was added
	assert.Equal(t, 2, len(pod.Spec.Volumes))
	addedVol := pod.Spec.Volumes[1]
	assert.Equal(t, tokenVolumeName, addedVol.Name)
	assert.Equal(t, hostSecretName, addedVol.Secret.SecretName)

	// Verify init container mount was added
	assert.Equal(t, 1, len(pod.Spec.InitContainers[0].VolumeMounts))
	assert.Equal(t, tokenVolumeName, pod.Spec.InitContainers[0].VolumeMounts[0].Name)
	assert.Equal(t, serviceAccountTokenMountPath, pod.Spec.InitContainers[0].VolumeMounts[0].MountPath)

	// Verify all container mounts were added
	for _, c := range pod.Spec.Containers {
		assert.Equal(t, 1, len(c.VolumeMounts), "container %s should have mount", c.Name)
		assert.Equal(t, tokenVolumeName, c.VolumeMounts[0].Name)
		assert.Equal(t, serviceAccountTokenMountPath, c.VolumeMounts[0].MountPath)
	}

	// Verify ephemeral container mounts were added
	for _, c := range pod.Spec.EphemeralContainers {
		assert.Equal(t, 1, len(c.VolumeMounts), "ephemeral container %s should have mount", c.Name)
		assert.Equal(t, tokenVolumeName, c.VolumeMounts[0].Name)
		assert.Equal(t, serviceAccountTokenMountPath, c.VolumeMounts[0].MountPath)
	}
}

func Test_kubeAccessTokenSecretName(t *testing.T) {
	assert.Equal(t, "k3k-my-pod-kube-api-access", kubeAccessTokenSecretName("my-pod"))
}

func Test_generateTokenSecretName(t *testing.T) {
	tests := []struct {
		name    string
		podName string
		token   *corev1.ServiceAccountTokenProjection
		want    string
	}{
		{
			name:    "empty token projection",
			podName: "my-pod",
			token:   &corev1.ServiceAccountTokenProjection{},
			want:    "k3k-my-pod-a3e070",
		},
		{
			name:    "with expiration",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				ExpirationSeconds: new(int64(3600)),
			},
			want: "k3k-my-pod-3600-31989f",
		},
		{
			name:    "with audience",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Audience: "vault",
			},
			want: "k3k-my-pod-vault-bc1ddb",
		},
		{
			name:    "with unsanitized audience",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Audience: "https://unsanitized-audience",
			},
			want: "k3k-my-pod-https-unsanitized-audience-596ff3",
		},
		{
			name:    "with uppercase audience",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Audience: "MyAudience",
			},
			want: "k3k-my-pod-myaudience-f73055",
		},
		{
			name:    "with path",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Path: "token",
			},
			want: "k3k-my-pod-token-7c40c3",
		},
		{
			name:    "with unsanitized path",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Path: "other-path/..",
			},
			want: "k3k-my-pod-other-path-f33213",
		},
		{
			name:    "with expiration, audience and path",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				ExpirationSeconds: new(int64(3600)),
				Audience:          "vault",
				Path:              "token",
			},
			want: "k3k-my-pod-vault-3600-token-bec46c",
		},
		{
			name:    "different pods get different names",
			podName: "my-other-pod",
			token: &corev1.ServiceAccountTokenProjection{
				ExpirationSeconds: new(int64(3600)),
				Audience:          "vault",
				Path:              "token",
			},
			want: "k3k-my-other-pod-vault-3600-token-adcdd4",
		},
		{
			name:    "with expiration, unsanitized audience and unsanitized path",
			podName: "my-serviceaccount",
			token: &corev1.ServiceAccountTokenProjection{
				ExpirationSeconds: new(int64(3600)),
				Audience:          "https://unsanitized-audience",
				Path:              "other-path/..",
			},
			// since the full name is longer than 63 characters, SafeConcatName() will truncate the
			// name and append the first 5 characters of the sha256-encoded full name
			want: "k3k-my-serviceaccount-https-unsanitized-audience-3600-oth-5db1a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateTokenSecretName(tt.podName, tt.token)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("pod names containing token attributes get different names", func(t *testing.T) {
		withAudience := generateTokenSecretName("app", &corev1.ServiceAccountTokenProjection{
			Audience:          "vault",
			ExpirationSeconds: new(int64(3600)),
			Path:              "token",
		})
		withoutAudience := generateTokenSecretName("app-vault", &corev1.ServiceAccountTokenProjection{
			ExpirationSeconds: new(int64(3600)),
			Path:              "token",
		})

		assert.NotEqual(t, withAudience, withoutAudience)
	})

	t.Run("attributes differing only before sanitization get different names", func(t *testing.T) {
		lowercase := generateTokenSecretName("my-pod", &corev1.ServiceAccountTokenProjection{Audience: "vault", Path: "token"})
		uppercase := generateTokenSecretName("my-pod", &corev1.ServiceAccountTokenProjection{Audience: "Vault", Path: "token"})

		assert.NotEqual(t, lowercase, uppercase)
	})

	t.Run("projected token named like the kube-api-access token gets a different name", func(t *testing.T) {
		got := generateTokenSecretName("my-pod", &corev1.ServiceAccountTokenProjection{Path: kubeAPIAccessPrefix})

		assert.NotEqual(t, kubeAccessTokenSecretName("my-pod"), got)
	})
}

func Test_tokenRefreshAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// refreshed at 80% of the lifetime
	assert.Equal(t, now.Add(8*time.Minute), tokenRefreshAfter(now, now.Add(10*time.Minute)))
	assert.Equal(t, now.Add(48*time.Minute), tokenRefreshAfter(now, now.Add(time.Hour)))

	// or after 24 hours for long lived tokens
	assert.Equal(t, now.Add(24*time.Hour), tokenRefreshAfter(now, now.Add(365*24*time.Hour)))

	// expired tokens are refreshed straight away
	assert.False(t, tokenRefreshAfter(now, now.Add(-time.Minute)).After(now))
}

func Test_tokenRefreshDue(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	secretWithAnnotation := func(value string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{tokenRefreshAfterAnnotation: value},
			},
		}
	}

	assert.True(t, tokenRefreshDue(&corev1.Secret{}, now), "missing annotation")
	assert.True(t, tokenRefreshDue(secretWithAnnotation("invalid"), now), "invalid annotation")
	assert.True(t, tokenRefreshDue(secretWithAnnotation("2025-12-31T23:59:00Z"), now), "refresh time passed")
	assert.True(t, tokenRefreshDue(secretWithAnnotation("2026-01-01T00:00:00Z"), now), "refresh time reached")
	assert.False(t, tokenRefreshDue(secretWithAnnotation("2026-01-01T00:01:00Z"), now), "refresh time not reached")
}

func Test_refreshPodTokens(t *testing.T) {
	ctx := context.Background()

	var requests []*authv1.TokenRequest

	p := newTokenTestProvider(t, &requests)
	p.clusterNamespace = "ns-test"

	virtualPod := newTokenTestPod("pod-a")
	hostPod := virtualPod.DeepCopy()

	hostSecretNames, err := p.transformTokens(ctx, virtualPod, hostPod)
	require.NoError(t, err)

	hostPod.Name = "host-pod"
	hostPod.Namespace = "ns-test"
	hostPod.UID = "host-pod-uid"

	require.NoError(t, p.setTokenSecretsOwner(ctx, hostPod, hostSecretNames))

	hostSecretKey := func(volume corev1.Volume) types.NamespacedName {
		name := ""
		if volume.Secret != nil {
			name = volume.Secret.SecretName
		} else {
			name = volume.Projected.Sources[0].Secret.Name
		}

		return types.NamespacedName{Name: name, Namespace: "ns-test"}
	}

	vaultKey := hostSecretKey(hostPod.Spec.Volumes[0])
	kubeAccessKey := hostSecretKey(hostPod.Spec.Volumes[1])

	getHostSecret := func(t *testing.T, key types.NamespacedName) *corev1.Secret {
		t.Helper()

		var secret corev1.Secret
		require.NoError(t, p.host.Client.Get(ctx, key, &secret))

		return &secret
	}

	t.Run("host secrets record when to refresh the tokens", func(t *testing.T) {
		for _, key := range []types.NamespacedName{vaultKey, kubeAccessKey} {
			secret := getHostSecret(t, key)

			refreshAfter, err := time.Parse(time.RFC3339, secret.Annotations[tokenRefreshAfterAnnotation])
			require.NoError(t, err)
			assert.True(t, refreshAfter.After(time.Now()), "secret %s", key.Name)
		}
	})

	t.Run("tokens not due are not refreshed", func(t *testing.T) {
		requests = nil

		require.NoError(t, p.refreshPodTokens(ctx, p.logger, virtualPod, hostPod))
		assert.Empty(t, requests)
	})

	t.Run("tokens due are refreshed", func(t *testing.T) {
		secret := getHostSecret(t, vaultKey)
		secret.Annotations[tokenRefreshAfterAnnotation] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
		secret.Data["token"] = []byte("expired")
		require.NoError(t, p.host.Client.Update(ctx, secret))

		requests = nil

		require.NoError(t, p.refreshPodTokens(ctx, p.logger, virtualPod, hostPod))

		// only the vault token was due
		require.Len(t, requests, 1)
		assert.Equal(t, []string{"vault"}, requests[0].Spec.Audiences)

		secret = getHostSecret(t, vaultKey)
		assert.Equal(t, []byte("pod-a/vault/600"), secret.Data["token"])
		assert.False(t, tokenRefreshDue(secret, time.Now()))
	})

	t.Run("missing owner references are set", func(t *testing.T) {
		secret := getHostSecret(t, kubeAccessKey)
		secret.OwnerReferences = nil
		require.NoError(t, p.host.Client.Update(ctx, secret))

		requests = nil

		require.NoError(t, p.refreshPodTokens(ctx, p.logger, virtualPod, hostPod))
		assert.Empty(t, requests, "tokens not due are not refreshed")

		secret = getHostSecret(t, kubeAccessKey)
		require.Len(t, secret.OwnerReferences, 1)
		assert.Equal(t, types.UID("host-pod-uid"), secret.OwnerReferences[0].UID)
	})

	t.Run("owner references are kept on refresh", func(t *testing.T) {
		secret := getHostSecret(t, vaultKey)
		secret.Annotations[tokenRefreshAfterAnnotation] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
		require.NoError(t, p.host.Client.Update(ctx, secret))

		require.NoError(t, p.refreshPodTokens(ctx, p.logger, virtualPod, hostPod))

		secret = getHostSecret(t, vaultKey)
		assert.False(t, tokenRefreshDue(secret, time.Now()))
		require.Len(t, secret.OwnerReferences, 1)
		assert.Equal(t, types.UID("host-pod-uid"), secret.OwnerReferences[0].UID)
	})

	t.Run("pods without token secrets are skipped", func(t *testing.T) {
		requests = nil

		require.NoError(t, p.refreshPodTokens(ctx, p.logger, newTokenTestPod("pod-without-secrets"), hostPod))
		assert.Empty(t, requests)
	})
}
