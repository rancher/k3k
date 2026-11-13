package syncer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	fakeEndpointSliceAddress  = "1.1.1.1"
	fakeEndpointSlicePort     = 8888
	fakeEndpointSlicePortName = "fake-port"
)

func TestEndpointSliceReconciler(t *testing.T) {
	virtualObject := newTestEndpointSlice("test-endpointslice-1")
	cluster := newTestCluster(func(c *v1beta1.Cluster) {
		c.Spec.Sync.EndpointSlices = v1beta1.EndpointSliceSyncConfig{Enabled: true}
	})
	syncer := newEndpointSliceReconciler(t, cluster, []client.Object{virtualObject})
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(virtualObject)}

	result, err := syncer.Reconcile(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)

	var gotVirtual discoveryv1.EndpointSlice
	require.NoError(t, syncer.VirtualClient.Get(t.Context(), request.NamespacedName, &gotVirtual))

	hostKey := syncer.Translator.NamespacedName(virtualObject)

	var gotHost discoveryv1.EndpointSlice
	require.NoError(t, syncer.HostClient.Get(t.Context(), hostKey, &gotHost))
	assert.Equal(t, virtualObject.AddressType, gotHost.AddressType)
	assert.Equal(t, cluster.UID, gotHost.OwnerReferences[0].UID)

	for i, endpoint := range virtualObject.Endpoints {
		for j, address := range endpoint.Addresses {
			assert.Less(t, i, len(gotHost.Endpoints))
			assert.Less(t, j, len(gotHost.Endpoints[i].Addresses))
			assert.Equal(t, address, gotHost.Endpoints[i].Addresses[j])
		}
	}

	for i, port := range virtualObject.Ports {
		assert.Len(t, gotHost.Ports, 1)
		assert.Equal(t, port.Name, gotHost.Ports[i].Name)
		assert.Equal(t, port.Protocol, gotHost.Ports[i].Protocol)
		assert.Equal(t, port.Port, gotHost.Ports[i].Port)
	}

	// update test
	require.NoError(t, syncer.VirtualClient.Get(t.Context(), request.NamespacedName, &gotVirtual))
	gotVirtual.Ports[0].Port = new(int32(8000))
	require.NoError(t, syncer.VirtualClient.Update(t.Context(), &gotVirtual))

	_, err = syncer.Reconcile(t.Context(), request)
	require.NoError(t, err)

	require.NoError(t, syncer.HostClient.Get(t.Context(), hostKey, &gotHost))
	assert.Len(t, gotHost.Ports, 1)
	assert.Equal(t, gotVirtual.Ports[0].Port, gotHost.Ports[0].Port)
}

func TestEndpointSliceReconcilerTargetRef(t *testing.T) {
	virtualObject := newTestEndpointSlice("test-endpointslice-targetref")
	virtualObject.Endpoints = []discoveryv1.Endpoint{
		{
			Addresses: []string{fakeEndpointSliceAddress},
			TargetRef: &corev1.ObjectReference{
				Kind:            "Pod",
				Name:            "my-pod",
				Namespace:       virtualNamespace,
				UID:             "virtual-pod-uid",
				ResourceVersion: "123",
			},
		},
		{
			Addresses: []string{"2.2.2.2"},
		},
	}

	cluster := newTestCluster(func(c *v1beta1.Cluster) {
		c.Spec.Sync.EndpointSlices = v1beta1.EndpointSliceSyncConfig{Enabled: true}
	})
	syncer := newEndpointSliceReconciler(t, cluster, []client.Object{virtualObject})
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(virtualObject)}

	_, err := syncer.Reconcile(t.Context(), request)
	require.NoError(t, err)

	var gotHost discoveryv1.EndpointSlice
	require.NoError(t, syncer.HostClient.Get(t.Context(), syncer.Translator.NamespacedName(virtualObject), &gotHost))
	require.Len(t, gotHost.Endpoints, 2)

	expected := &corev1.ObjectReference{
		Kind:      "Pod",
		Name:      syncer.Translator.TranslateName(virtualNamespace, "my-pod"),
		Namespace: testClusterNamespace,
	}
	assert.Equal(t, expected, gotHost.Endpoints[0].TargetRef)
	assert.Nil(t, gotHost.Endpoints[1].TargetRef)
}

func TestEndpointSliceFilterResources(t *testing.T) {
	withLabels := func(labels map[string]string) *discoveryv1.EndpointSlice {
		eps := newTestEndpointSlice("test-endpointslice")
		eps.Labels = labels

		return eps
	}

	tests := []struct {
		name             string
		object           *discoveryv1.EndpointSlice
		servicesDisabled bool
		filtered         bool
	}{
		{
			name:     "with service name",
			object:   withLabels(map[string]string{discoveryv1.LabelServiceName: "my-svc"}),
			filtered: true,
		},
		{
			name:     "without service name",
			object:   withLabels(map[string]string{"special-label": "e2e"}),
			filtered: false,
		},
		{
			name:     "empty service name",
			object:   withLabels(map[string]string{discoveryv1.LabelServiceName: ""}),
			filtered: false,
		},
		{
			name: "managed by endpointslice controller",
			object: withLabels(map[string]string{
				discoveryv1.LabelServiceName: "my-svc",
				discoveryv1.LabelManagedBy:   kubernetesEndpointSliceControllerName,
			}),
			filtered: false,
		},
		{
			name:             "services sync disabled",
			object:           withLabels(map[string]string{discoveryv1.LabelServiceName: "my-svc"}),
			servicesDisabled: true,
			filtered:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := newTestCluster(func(c *v1beta1.Cluster) {
				c.Spec.Sync.EndpointSlices = v1beta1.EndpointSliceSyncConfig{Enabled: true}
				c.Spec.Sync.Services = v1beta1.ServiceSyncConfig{Enabled: !tt.servicesDisabled}
			})
			syncer := newEndpointSliceReconciler(t, cluster, nil)
			assert.Equal(t, tt.filtered, syncer.filterResources(tt.object))
		})
	}
}

func newEndpointSliceReconciler(t *testing.T, cluster *v1beta1.Cluster, virtualObjects []client.Object, hostObjects ...client.Object) *EndpointSliceReconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))
	require.NoError(t, discoveryv1.AddToScheme(scheme))

	hostObjects = append(hostObjects, cluster)

	return &EndpointSliceReconciler{
		Context: &Context{
			VirtualClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(virtualObjects...).Build(),
			HostClient:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(hostObjects...).Build(),
			Translator: translate.ToHostTranslator{
				ClusterName:      testClusterName,
				ClusterNamespace: testClusterNamespace,
			},
			ClusterName:      testClusterName,
			ClusterNamespace: testClusterNamespace,
		},
	}
}

func newTestEndpointSlice(name string) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: virtualNamespace,
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports: []discoveryv1.EndpointPort{
			{
				Name:     new(fakeEndpointSlicePortName),
				Protocol: new(corev1.ProtocolTCP),
				Port:     new(int32(fakeEndpointSlicePort)),
			},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses: []string{fakeEndpointSliceAddress},
			},
		},
	}
}
