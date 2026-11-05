package cmds

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
)

func Test_printClusterDetails(t *testing.T) {
	tests := []struct {
		name    string
		cluster *v1beta1.Cluster
		want    string
		wantErr bool
	}{
		{
			name: "simple cluster",
			cluster: &v1beta1.Cluster{
				Spec: v1beta1.ClusterSpec{
					Mode:    v1beta1.SharedClusterMode,
					Version: "123",
					Persistence: v1beta1.PersistenceConfig{
						Type: v1beta1.DynamicPersistenceMode,
					},
				},
				Status: v1beta1.ClusterStatus{
					HostVersion: "456",
				},
			},
			want: `Cluster details:
  Mode: shared
  Servers: 0
  Version: 123 (Host: 456)
  Persistence:
    Type: dynamic`,
		},
		{
			name: "simple cluster with no version",
			cluster: &v1beta1.Cluster{
				Spec: v1beta1.ClusterSpec{
					Mode: v1beta1.SharedClusterMode,
					Persistence: v1beta1.PersistenceConfig{
						Type: v1beta1.DynamicPersistenceMode,
					},
				},
				Status: v1beta1.ClusterStatus{
					HostVersion: "456",
				},
			},
			want: `Cluster details:
  Mode: shared
  Servers: 0
  Version: 456 (Host: 456)
  Persistence:
    Type: dynamic`,
		},
		{
			name: "cluster with agents",
			cluster: &v1beta1.Cluster{
				Spec: v1beta1.ClusterSpec{
					Mode:   v1beta1.SharedClusterMode,
					Agents: new(int32(3)),
					Persistence: v1beta1.PersistenceConfig{
						Type:               v1beta1.DynamicPersistenceMode,
						StorageClassName:   new("local-path"),
						StorageRequestSize: new(resource.MustParse("3G")),
					},
				},
				Status: v1beta1.ClusterStatus{
					HostVersion: "456",
				},
			},
			want: `Cluster details:
  Mode: shared
  Servers: 0
  Agents: 3
  Version: 456 (Host: 456)
  Persistence:
    Type: dynamic
    StorageClass: local-path
    Size: 3G`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterDetails, err := getClusterDetails(tt.cluster)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, clusterDetails)
		})
	}
}

func Test_extractHost(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		override  string
		want      string
	}{
		{
			name:      "plain host",
			serverURL: "https://example.com",
			want:      "example.com",
		},
		{
			name:      "host with port",
			serverURL: "https://example.com:6443",
			want:      "example.com",
		},
		{
			name:      "ipv4 with port",
			serverURL: "https://192.0.2.1:6443",
			want:      "192.0.2.1",
		},
		{
			name:      "ipv6 without port",
			serverURL: "https://[2001:db8::1]",
			want:      "2001:db8::1",
		},
		{
			name:      "ipv6 with port",
			serverURL: "https://[2001:db8::1]:6443",
			want:      "2001:db8::1",
		},
		{
			name:      "override wins",
			serverURL: "https://example.com:6443",
			override:  "custom.host",
			want:      "custom.host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, err := extractHost(tt.serverURL, tt.override)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, host)
		})
	}
}

func newTestTokenCluster() *v1beta1.Cluster {
	return &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "mycluster", Namespace: "k3k-mycluster", UID: types.UID("cluster-uid")},
		Spec: v1beta1.ClusterSpec{
			TokenSecretRef: &corev1.SecretReference{Name: k3kcluster.TokenSecretName("mycluster"), Namespace: "k3k-mycluster"},
		},
	}
}

func Test_createCluster(t *testing.T) {
	clustersResource := schema.GroupResource{Group: v1beta1.SchemeGroupVersion.Group, Resource: "clusters"}

	tests := []struct {
		name            string
		token           string
		existingObjects func(cluster *v1beta1.Cluster) []client.Object
		// createErr is returned when creating the cluster, after creating it if persistCluster is set
		createErr      error
		persistCluster bool
		// deleteCluster deletes the cluster right after creating it, before it owns the token secret
		deleteCluster    bool
		wantErr          string
		wantSecret       bool
		wantSecretOwned  bool
		wantClusterExist bool
	}{
		{
			name:             "creates the cluster and its owned token secret",
			token:            "mytoken",
			wantSecret:       true,
			wantSecretOwned:  true,
			wantClusterExist: true,
		},
		{
			name:             "creates the cluster without a token",
			wantClusterExist: true,
		},
		{
			// a previous run created the secret and the cluster, but failed to set the owner
			name:  "adopts the token secret of a previous run",
			token: "mytoken",
			existingObjects: func(cluster *v1beta1.Cluster) []client.Object {
				tokenSecret := k3kcluster.TokenSecretObj("mytoken", cluster.Name, cluster.Namespace)
				return []client.Object{cluster.DeepCopy(), &tokenSecret}
			},
			wantSecret:       true,
			wantSecretOwned:  true,
			wantClusterExist: true,
		},
		{
			name:  "fails if the token secret holds another token",
			token: "mytoken",
			existingObjects: func(cluster *v1beta1.Cluster) []client.Object {
				tokenSecret := k3kcluster.TokenSecretObj("othertoken", cluster.Name, cluster.Namespace)
				return []client.Object{&tokenSecret}
			},
			wantErr:    "already exists with a different token",
			wantSecret: true,
		},
		{
			name:      "deletes the created token secret if the cluster is rejected",
			token:     "mytoken",
			createErr: apierrors.NewForbidden(clustersResource, "mycluster", errors.New("denied")),
			wantErr:   "denied",
		},
		{
			name:       "keeps the token secret if the cluster creation fails",
			token:      "mytoken",
			createErr:  errors.New("connection reset by peer"),
			wantErr:    "run the command again to retry",
			wantSecret: true,
		},
		{
			// the cluster is created, but its response is lost
			name:             "keeps the token secret if the cluster creation times out",
			token:            "mytoken",
			createErr:        apierrors.NewTimeoutError("request timed out", 0),
			persistCluster:   true,
			wantErr:          "run the command again to retry",
			wantSecret:       true,
			wantClusterExist: true,
		},
		{
			name:          "deletes the created token secret if the cluster is deleted before owning it",
			token:         "mytoken",
			deleteCluster: true,
			wantErr:       "the cluster was deleted while being created",
		},
		{
			// the secret was not created by this run, so it's not deleted
			name:  "keeps the token secret of a previous run if the cluster is deleted before owning it",
			token: "mytoken",
			existingObjects: func(cluster *v1beta1.Cluster) []client.Object {
				tokenSecret := k3kcluster.TokenSecretObj("mytoken", cluster.Name, cluster.Namespace)
				return []client.Object{&tokenSecret}
			},
			deleteCluster: true,
			wantErr:       "run the command again to retry",
			wantSecret:    true,
		},
		{
			name:  "deletes the created token secret if the cluster is being deleted",
			token: "mytoken",
			existingObjects: func(cluster *v1beta1.Cluster) []client.Object {
				return []client.Object{deletingCluster(cluster)}
			},
			wantErr:          "the cluster is being deleted",
			wantClusterExist: true,
		},
		{
			// the token secret is deleted with the cluster
			name:  "keeps the token secret owned by a cluster being deleted",
			token: "mytoken",
			existingObjects: func(cluster *v1beta1.Cluster) []client.Object {
				tokenSecret := ownedTokenSecret(t, cluster)
				return []client.Object{deletingCluster(cluster), tokenSecret}
			},
			wantErr:          "the cluster is being deleted",
			wantSecret:       true,
			wantSecretOwned:  true,
			wantClusterExist: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := newTestTokenCluster()

			builder := fake.NewClientBuilder().WithScheme(newTestScheme(t))
			if tt.existingObjects != nil {
				builder = builder.WithObjects(tt.existingObjects(cluster)...)
			}

			if tt.createErr != nil {
				builder = builder.WithInterceptorFuncs(failClusterCreate(tt.createErr, tt.persistCluster))
			}

			if tt.deleteCluster {
				builder = builder.WithInterceptorFuncs(deleteClusterAfterCreate())
			}

			k8sClient := builder.Build()

			err := createCluster(t.Context(), k8sClient, cluster, tt.token)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			var gotCluster v1beta1.Cluster

			err = k8sClient.Get(t.Context(), client.ObjectKeyFromObject(cluster), &gotCluster)
			assert.Equal(t, tt.wantClusterExist, err == nil, "cluster exists")

			var gotSecret corev1.Secret

			err = k8sClient.Get(t.Context(), types.NamespacedName{Name: k3kcluster.TokenSecretName(cluster.Name), Namespace: cluster.Namespace}, &gotSecret)
			assert.Equal(t, tt.wantSecret, err == nil, "token secret exists")

			if tt.wantSecretOwned {
				ref := metav1.GetControllerOf(&gotSecret)
				require.NotNil(t, ref)
				assert.Equal(t, cluster.UID, ref.UID)
			}
		})
	}
}

func Test_createCluster_rerunAfterLostResponse(t *testing.T) {
	cluster := newTestTokenCluster()

	k8sClient := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithInterceptorFuncs(failClusterCreate(apierrors.NewTimeoutError("request timed out", 0), true)).
		Build()

	err := createCluster(t.Context(), k8sClient, cluster.DeepCopy(), "mytoken")
	require.ErrorContains(t, err, "run the command again to retry")

	// the cluster already exists, so the interceptor is not reached
	require.NoError(t, createCluster(t.Context(), k8sClient, cluster.DeepCopy(), "mytoken"))

	var got corev1.Secret
	require.NoError(t, k8sClient.Get(t.Context(), types.NamespacedName{Name: k3kcluster.TokenSecretName(cluster.Name), Namespace: cluster.Namespace}, &got))

	ref := metav1.GetControllerOf(&got)
	require.NotNil(t, ref)
	assert.Equal(t, cluster.UID, ref.UID)
}

func Test_setTokenSecretOwner(t *testing.T) {
	cluster := newTestTokenCluster()

	ownerRef := func(apiVersion, kind, name, uid string, controller bool) metav1.OwnerReference {
		return metav1.OwnerReference{APIVersion: apiVersion, Kind: kind, Name: name, UID: types.UID(uid), Controller: ptr.To(controller)}
	}

	clusterAPIVersion := v1beta1.SchemeGroupVersion.String()
	unrelatedOwner := ownerRef("v1", "ConfigMap", "unrelated", "unrelated-uid", false)

	tests := []struct {
		name          string
		ownerRefs     []metav1.OwnerReference
		wantOwnerUIDs []types.UID
		wantErr       bool
	}{
		{
			name:          "secret without owner",
			wantOwnerUIDs: []types.UID{cluster.UID},
		},
		{
			name:          "secret already controlled by the cluster",
			ownerRefs:     []metav1.OwnerReference{ownerRef(clusterAPIVersion, "Cluster", cluster.Name, string(cluster.UID), true)},
			wantOwnerUIDs: []types.UID{cluster.UID},
		},
		{
			// the cluster with this name was deleted, as the current cluster has another UID
			name:          "stale reference to a deleted cluster with the same name",
			ownerRefs:     []metav1.OwnerReference{unrelatedOwner, ownerRef(clusterAPIVersion, "Cluster", cluster.Name, "stale-cluster-uid", true)},
			wantOwnerUIDs: []types.UID{"unrelated-uid", cluster.UID},
		},
		{
			name:          "secret controlled by another cluster",
			ownerRefs:     []metav1.OwnerReference{ownerRef(clusterAPIVersion, "Cluster", "othercluster", "other-cluster-uid", true)},
			wantOwnerUIDs: []types.UID{"other-cluster-uid"},
			wantErr:       true,
		},
		{
			name:          "secret controlled by another kind with the same name",
			ownerRefs:     []metav1.OwnerReference{ownerRef("v1", "ConfigMap", cluster.Name, "configmap-uid", true)},
			wantOwnerUIDs: []types.UID{"configmap-uid"},
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret := k3kcluster.TokenSecretObj("mytoken", cluster.Name, cluster.Namespace)
			secret.OwnerReferences = tt.ownerRefs

			k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, &secret).Build()

			err := setTokenSecretOwner(t.Context(), k8sClient, cluster)

			if tt.wantErr {
				var alreadyOwnedErr *controllerutil.AlreadyOwnedError
				require.ErrorAs(t, err, &alreadyOwnedErr)
			} else {
				require.NoError(t, err)
			}

			var got corev1.Secret
			require.NoError(t, k8sClient.Get(t.Context(), client.ObjectKeyFromObject(&secret), &got))

			var gotOwnerUIDs []types.UID
			for _, ref := range got.OwnerReferences {
				gotOwnerUIDs = append(gotOwnerUIDs, ref.UID)
			}

			assert.Equal(t, tt.wantOwnerUIDs, gotOwnerUIDs)
		})
	}
}

func TestSetTokenSecretOwnerRetriesOnConflict(t *testing.T) {
	cluster := newTestTokenCluster()
	secret := k3kcluster.TokenSecretObj("mytoken", cluster.Name, cluster.Namespace)

	var updates int

	k8sClient := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(cluster, &secret).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				updates++
				if updates == 1 {
					return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, obj.GetName(), errors.New("conflict"))
				}

				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()

	require.NoError(t, setTokenSecretOwner(t.Context(), k8sClient, cluster))
	assert.Equal(t, 2, updates)

	var got corev1.Secret
	require.NoError(t, k8sClient.Get(t.Context(), client.ObjectKeyFromObject(&secret), &got))

	ref := metav1.GetControllerOf(&got)
	require.NotNil(t, ref)
	assert.Equal(t, cluster.UID, ref.UID)
}

// failClusterCreate returns interceptor functions failing the creation of a new cluster with
// err, after creating it if persist is set.
func failClusterCreate(createErr error, persist bool) interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*v1beta1.Cluster); !ok {
				return c.Create(ctx, obj, opts...)
			}

			if !persist {
				return createErr
			}

			if err := c.Create(ctx, obj, opts...); err != nil {
				return err
			}

			return createErr
		},
	}
}

// deleteClusterAfterCreate returns interceptors deleting a cluster right after creating it,
// as if it was deleted by someone else while the cli was still creating it.
func deleteClusterAfterCreate() interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if err := c.Create(ctx, obj, opts...); err != nil {
				return err
			}

			if _, ok := obj.(*v1beta1.Cluster); !ok {
				return nil
			}

			return c.Delete(ctx, obj)
		},
	}
}

func Test_createCluster_clusterRecreatedWhileSettingOwner(t *testing.T) {
	cluster := newTestTokenCluster()

	recreated := newTestTokenCluster()
	recreated.UID = types.UID("recreated-uid")

	var recreatedCluster bool

	// the first update of the token secret conflicts: in the meantime the cluster was deleted,
	// created again, and the new cluster set as the owner of the secret
	k8sClient := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, ok := obj.(*corev1.Secret); !ok || recreatedCluster {
					return c.Update(ctx, obj, opts...)
				}

				recreatedCluster = true

				require.NoError(t, c.Delete(ctx, cluster.DeepCopy()))
				require.NoError(t, c.Create(ctx, recreated.DeepCopy()))

				var tokenSecret corev1.Secret
				require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), &tokenSecret))
				require.NoError(t, controllerutil.SetControllerReference(recreated, &tokenSecret, c.Scheme()))
				require.NoError(t, c.Update(ctx, &tokenSecret))

				return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, obj.GetName(), errors.New("modified"))
			},
		}).
		Build()

	require.NoError(t, createCluster(t.Context(), k8sClient, cluster.DeepCopy(), "mytoken"))
	require.True(t, recreatedCluster)

	var got corev1.Secret
	require.NoError(t, k8sClient.Get(t.Context(), types.NamespacedName{Name: k3kcluster.TokenSecretName(cluster.Name), Namespace: cluster.Namespace}, &got))

	ref := metav1.GetControllerOf(&got)
	require.NotNil(t, ref)
	assert.Equal(t, recreated.UID, ref.UID, "the token secret should be owned by the current cluster")
}

// deletingCluster returns a copy of the cluster being deleted, kept by a finalizer.
func deletingCluster(cluster *v1beta1.Cluster) *v1beta1.Cluster {
	deleting := cluster.DeepCopy()
	deleting.Finalizers = []string{"cluster.k3k.io/finalizer"}
	deleting.DeletionTimestamp = &metav1.Time{Time: time.Now()}

	return deleting
}

// ownedTokenSecret returns the token secret of the cluster, controlled by it.
func ownedTokenSecret(t *testing.T, cluster *v1beta1.Cluster) *corev1.Secret {
	t.Helper()

	tokenSecret := k3kcluster.TokenSecretObj("mytoken", cluster.Name, cluster.Namespace)
	require.NoError(t, controllerutil.SetControllerReference(cluster, &tokenSecret, newTestScheme(t)))

	return &tokenSecret
}
