package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
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
		ObjectMeta: metav1.ObjectMeta{Name: kubeRootCAConfigMapName, Namespace: "default"},
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

	p := &Provider{
		host: ClusterContext{
			Client: ctrlfake.NewClientBuilder().WithObjects(hostSecret).Build(),
		},
	}

	require.NoError(t, p.setTokenSecretsOwner(ctx, hostPod, []string{"host-secret"}))

	require.NoError(t, p.host.Client.Get(ctx, client.ObjectKeyFromObject(hostSecret), hostSecret))
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
			want:    "k3k-my-pod",
		},
		{
			name:    "with expiration",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				ExpirationSeconds: new(int64(3600)),
			},
			want: "k3k-my-pod-3600",
		},
		{
			name:    "with audience",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Audience: "vault",
			},
			want: "k3k-my-pod-vault",
		},
		{
			name:    "with unsanitized audience",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Audience: "https://unsanitized-audience",
			},
			want: "k3k-my-pod-https-unsanitized-audience-23d739",
		},
		{
			name:    "with uppercase audience",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Audience: "MyAudience",
			},
			want: "k3k-my-pod-myaudience-753a30",
		},
		{
			name:    "with path",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Path: "token",
			},
			want: "k3k-my-pod-token",
		},
		{
			name:    "with unsanitized path",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				Path: "other-path/..",
			},
			want: "k3k-my-pod-other-path-0e329c",
		},
		{
			name:    "with expiration, audience and path",
			podName: "my-pod",
			token: &corev1.ServiceAccountTokenProjection{
				ExpirationSeconds: new(int64(3600)),
				Audience:          "vault",
				Path:              "token",
			},
			want: "k3k-my-pod-vault-3600-token",
		},
		{
			name:    "different pods get different names",
			podName: "my-other-pod",
			token: &corev1.ServiceAccountTokenProjection{
				ExpirationSeconds: new(int64(3600)),
				Audience:          "vault",
				Path:              "token",
			},
			want: "k3k-my-other-pod-vault-3600-token",
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
			want: "k3k-my-serviceaccount-https-unsanitized-audience-23d739-3-39d9d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateTokenSecretName(tt.podName, tt.token)
			assert.Equal(t, tt.want, got)
		})
	}
}
