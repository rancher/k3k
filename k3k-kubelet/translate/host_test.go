package translate

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func newTranslator(clusterName, clusterNamespace string) *ToHostTranslator {
	return &ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}
}

func TestNewHostTranslator(t *testing.T) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "my-namespace",
		},
	}

	tr := NewHostTranslator(cluster)
	assert.Equal(t, "my-cluster", tr.ClusterName)
	assert.Equal(t, "my-namespace", tr.ClusterNamespace)
}

func TestTranslateTo(t *testing.T) {
	tests := []struct {
		name             string
		clusterName      string
		clusterNamespace string
		obj              *corev1.ConfigMap
		verify           func(t *testing.T, obj *corev1.ConfigMap, tr *ToHostTranslator)
	}{
		{
			name:             "sets tracking annotations and label",
			clusterName:      "mycluster",
			clusterNamespace: "host-ns",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cm",
					Namespace: "virt-ns",
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap, tr *ToHostTranslator) {
				assert.Equal(t, "my-cm", obj.Annotations[ResourceNameAnnotation])
				assert.Equal(t, "virt-ns", obj.Annotations[ResourceNamespaceAnnotation])
				assert.Equal(t, "mycluster", obj.Labels[ClusterNameLabel])
			},
		},
		{
			name:             "sets translated name and host namespace",
			clusterName:      "mycluster",
			clusterNamespace: "host-ns",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cm",
					Namespace: "virt-ns",
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap, tr *ToHostTranslator) {
				assert.Equal(t, tr.TranslateName("virt-ns", "my-cm"), obj.Name)
				assert.Equal(t, "host-ns", obj.Namespace)
			},
		},
		{
			name:             "clears resource version and UID",
			clusterName:      "mycluster",
			clusterNamespace: "host-ns",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "my-cm",
					Namespace:       "virt-ns",
					ResourceVersion: "123",
					UID:             types.UID("abc-uid"),
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap, _ *ToHostTranslator) {
				assert.Empty(t, obj.ResourceVersion)
				assert.Empty(t, obj.UID)
			},
		},
		{
			name:             "clears owner references and finalizers",
			clusterName:      "mycluster",
			clusterNamespace: "host-ns",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cm",
					Namespace: "virt-ns",
					OwnerReferences: []metav1.OwnerReference{
						{Name: "owner", UID: "owner-uid"},
					},
					Finalizers: []string{"my-finalizer"},
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap, _ *ToHostTranslator) {
				assert.Nil(t, obj.OwnerReferences)
				assert.Nil(t, obj.Finalizers)
			},
		},
		{
			name:             "preserves existing annotations and labels",
			clusterName:      "mycluster",
			clusterNamespace: "host-ns",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-cm",
					Namespace:   "virt-ns",
					Annotations: map[string]string{"existing-annotation": "value"},
					Labels:      map[string]string{"existing-label": "value"},
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap, _ *ToHostTranslator) {
				assert.Equal(t, "value", obj.Annotations["existing-annotation"])
				assert.Equal(t, "value", obj.Labels["existing-label"])
			},
		},
		{
			name:             "handles nil annotations and labels",
			clusterName:      "mycluster",
			clusterNamespace: "host-ns",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cm",
					Namespace: "virt-ns",
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap, _ *ToHostTranslator) {
				assert.NotNil(t, obj.Annotations)
				assert.NotNil(t, obj.Labels)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTranslator(tt.clusterName, tt.clusterNamespace)
			tr.TranslateTo(tt.obj)
			tt.verify(t, tt.obj, tr)
		})
	}
}

func TestTranslateFrom(t *testing.T) {
	tests := []struct {
		name   string
		obj    *corev1.ConfigMap
		verify func(t *testing.T, obj *corev1.ConfigMap)
	}{
		{
			name: "restores name and namespace from annotations",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "translated-name",
					Namespace: "host-ns",
					Annotations: map[string]string{
						ResourceNameAnnotation:      "original-name",
						ResourceNamespaceAnnotation: "original-ns",
					},
					Labels: map[string]string{
						ClusterNameLabel: "mycluster",
					},
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap) {
				assert.Equal(t, "original-name", obj.Name)
				assert.Equal(t, "original-ns", obj.Namespace)
			},
		},
		{
			name: "removes tracking annotations",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "translated-name",
					Namespace: "host-ns",
					Annotations: map[string]string{
						ResourceNameAnnotation:      "original-name",
						ResourceNamespaceAnnotation: "original-ns",
						"other-annotation":          "keep-me",
					},
					Labels: map[string]string{
						ClusterNameLabel: "mycluster",
					},
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap) {
				assert.NotContains(t, obj.Annotations, ResourceNameAnnotation)
				assert.NotContains(t, obj.Annotations, ResourceNamespaceAnnotation)
				assert.Equal(t, "keep-me", obj.Annotations["other-annotation"])
			},
		},
		{
			name: "removes cluster name label",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "translated-name",
					Namespace: "host-ns",
					Annotations: map[string]string{
						ResourceNameAnnotation:      "original-name",
						ResourceNamespaceAnnotation: "original-ns",
					},
					Labels: map[string]string{
						ClusterNameLabel: "mycluster",
						"other-label":    "keep-me",
					},
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap) {
				assert.NotContains(t, obj.Labels, ClusterNameLabel)
				assert.Equal(t, "keep-me", obj.Labels["other-label"])
			},
		},
		{
			name: "clears resource version and UID",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "translated-name",
					Namespace:       "host-ns",
					ResourceVersion: "999",
					UID:             types.UID("some-uid"),
					Annotations: map[string]string{
						ResourceNameAnnotation:      "original-name",
						ResourceNamespaceAnnotation: "original-ns",
					},
					Labels: map[string]string{
						ClusterNameLabel: "mycluster",
					},
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap) {
				assert.Empty(t, obj.ResourceVersion)
				assert.Empty(t, obj.UID)
			},
		},
		{
			name: "clears owner references",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "translated-name",
					Namespace: "host-ns",
					Annotations: map[string]string{
						ResourceNameAnnotation:      "original-name",
						ResourceNamespaceAnnotation: "original-ns",
					},
					Labels: map[string]string{
						ClusterNameLabel: "mycluster",
					},
					OwnerReferences: []metav1.OwnerReference{
						{Name: "host-owner", UID: "host-owner-uid"},
					},
				},
			},
			verify: func(t *testing.T, obj *corev1.ConfigMap) {
				assert.Nil(t, obj.OwnerReferences)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTranslator("mycluster", "host-ns")
			tr.TranslateFrom(tt.obj)
			tt.verify(t, tt.obj)
		})
	}
}

func TestTranslateName(t *testing.T) {
	tr := newTranslator("mycluster", "host-ns")

	tests := []struct {
		name      string
		namespace string
		objName   string
		verify    func(t *testing.T, result string)
	}{
		{
			name:      "namespaced resource produces valid name",
			namespace: "virt-ns",
			objName:   "my-cm",
			verify: func(t *testing.T, result string) {
				assert.NotEmpty(t, result)
				assert.LessOrEqual(t, len(result), 63)
			},
		},
		{
			name:      "non-namespaced resource omits namespace from name",
			namespace: "",
			objName:   "my-priority-class",
			verify: func(t *testing.T, result string) {
				assert.NotEmpty(t, result)
				assert.LessOrEqual(t, len(result), 63)
				// Without a namespace segment, name should not contain "--"
				assert.NotContains(t, result, "--")
			},
		},
		{
			name:      "namespaced result encodes suffix deterministically",
			namespace: "virt-ns",
			objName:   "my-cm",
			verify: func(t *testing.T, result string) {
				// Calling TranslateName again must return the same value
				result2 := tr.TranslateName("virt-ns", "my-cm")
				assert.Equal(t, result, result2)
			},
		},
		{
			name:      "long name suffix is hex-encoded",
			namespace: "virt-ns",
			objName:   strings.Repeat("b", 50),
			verify: func(t *testing.T, result string) {
				// For names that exceed 63 chars, SafeConcatName appends a short hex snippet
				// (5 or 6 chars) from SHA256. Pad to even length before decoding so that
				// odd-length suffixes are accepted.
				parts := strings.Split(result, "-")
				suffix := parts[len(parts)-1]

				padded := suffix
				if len(padded)%2 != 0 {
					padded = "0" + padded
				}

				_, err := hex.DecodeString(padded)
				assert.NoError(t, err, "suffix should be valid hex")
			},
		},
		{
			name:      "different names produce different results",
			namespace: "virt-ns",
			objName:   "my-cm",
			verify: func(t *testing.T, result string) {
				other := tr.TranslateName("virt-ns", "other-cm")
				assert.NotEqual(t, result, other)
			},
		},
		{
			name:      "different namespaces produce different results",
			namespace: "virt-ns",
			objName:   "my-cm",
			verify: func(t *testing.T, result string) {
				other := tr.TranslateName("other-ns", "my-cm")
				assert.NotEqual(t, result, other)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.TranslateName(tt.namespace, tt.objName)
			tt.verify(t, result)
		})
	}
}

func TestTranslateNameLong(t *testing.T) {
	tr := newTranslator("mycluster", "host-ns")

	longName := strings.Repeat("a", 60)
	result := tr.TranslateName("virt-ns", longName)

	assert.LessOrEqual(t, len(result), 63, "translated name must fit within 63 characters")
}

func TestNamespacedName(t *testing.T) {
	tr := newTranslator("mycluster", "host-ns")

	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cm",
			Namespace: "virt-ns",
		},
	}

	nn := tr.NamespacedName(obj)

	assert.Equal(t, "host-ns", nn.Namespace)
	assert.Equal(t, tr.TranslateName("virt-ns", "my-cm"), nn.Name)
}

func TestTranslateToFromRoundtrip(t *testing.T) {
	tr := newTranslator("mycluster", "host-ns")

	original := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cm",
			Namespace: "virt-ns",
			Annotations: map[string]string{
				"user-annotation": "user-value",
			},
			Labels: map[string]string{
				"user-label": "user-value",
			},
		},
	}

	// Deep-copy the original metadata we want to verify round-trip for
	origName := original.Name
	origNamespace := original.Namespace

	tr.TranslateTo(original)

	// After TranslateTo the name/namespace should be host-side values
	assert.NotEqual(t, origName, original.Name)
	assert.Equal(t, "host-ns", original.Namespace)

	tr.TranslateFrom(original)

	// After TranslateFrom we should be back to the virtual-cluster values
	assert.Equal(t, origName, original.Name)
	assert.Equal(t, origNamespace, original.Namespace)
	assert.Equal(t, "user-value", original.Annotations["user-annotation"])
	assert.Equal(t, "user-value", original.Labels["user-label"])
}
