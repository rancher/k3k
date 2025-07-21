package translate

import (
	"encoding/hex"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/controller"
)

const (
	// ClusterNameLabel is the key for the label that contains the name of the virtual cluster
	// this resource was made in
	ClusterNameLabel = "k3k.io/clusterName"
	// ResourceNameAnnotation is the key for the annotation that contains the original name of this
	// resource in the virtual cluster
	ResourceNameAnnotation = "k3k.io/name"
	// ResourceNamespaceAnnotation is the key for the annotation that contains the original namespace of this
	// resource in the virtual cluster
	ResourceNamespaceAnnotation = "k3k.io/namespace"
	// MetadataNameField is the downwardapi field for object's name
	MetadataNameField = "metadata.name"
	// MetadataNamespaceField is the downward field for the object's namespace
	MetadataNamespaceField = "metadata.namespace"
)

type ToHostTranslator struct {
	// ClusterName is the name of the virtual cluster whose resources we are
	// translating to a host cluster
	ClusterName string
	// ClusterNamespace is the namespace of the virtual cluster whose resources
	// we are translating to a host cluster
	ClusterNamespace string
}

// Translate translates a virtual cluster object to a host cluster object. This should only be used for
// static resources such as configmaps/secrets, and not for things like pods (which can reference other
// objects). Note that this won't set host-cluster values (like resource version) so when updating you
// may need to fetch the existing value and do some combination before using this.
func (t *ToHostTranslator) TranslateTo(obj client.Object) {
	// owning objects may be in the virtual cluster, but may not be in the host cluster
	obj.SetOwnerReferences(nil)
	// add some annotations to make it easier to track source object
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	annotations[ResourceNameAnnotation] = obj.GetName()
	annotations[ResourceNamespaceAnnotation] = obj.GetNamespace()
	obj.SetAnnotations(annotations)

	// add a label to quickly identify objects owned by a given virtual cluster
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels[ClusterNameLabel] = t.ClusterName
	obj.SetLabels(labels)

	// resource version/UID won't match what's in the host cluster.
	obj.SetResourceVersion("")
	obj.SetUID("")

	// set the name and the namespace so that this goes in the proper host namespace
	// and doesn't collide with other resources
	obj.SetName(t.TranslateName(obj.GetNamespace(), obj.GetName()))
	obj.SetNamespace(t.ClusterNamespace)
	obj.SetFinalizers(nil)
}

func (t *ToHostTranslator) TranslateFrom(obj client.Object) {
	// owning objects may be in the virtual cluster, but may not be in the host cluster
	obj.SetOwnerReferences(nil)

	// remove the annotations added to track original name
	annotations := obj.GetAnnotations()
	// TODO: It's possible that this was erased by a change on the host cluster
	// In this case, we need to have some sort of fallback or error return
	name := annotations[ResourceNameAnnotation]
	namespace := annotations[ResourceNamespaceAnnotation]

	obj.SetName(name)
	obj.SetNamespace(namespace)
	delete(annotations, ResourceNameAnnotation)
	delete(annotations, ResourceNamespaceAnnotation)
	obj.SetAnnotations(annotations)

	// remove the clusteName tracking label
	labels := obj.GetLabels()
	delete(labels, ClusterNameLabel)
	obj.SetLabels(labels)

	// resource version/UID won't match what's in the virtual cluster.
	obj.SetResourceVersion("")
	obj.SetUID("")
}

// TranslateName returns the name of the resource in the host cluster. Will not update the object with this name.
func (t *ToHostTranslator) TranslateName(namespace string, name string) string {
	var names []string

	// some resources are not namespaced (i.e. priorityclasses)
	/// for these resources we skip the namespace to avoid having a name like: prioritclass--cluster-123
	if namespace == "" {
		names = []string{name, t.ClusterName}
	} else {
		names = []string{name, namespace, t.ClusterName}
	}

	// we need to come up with a name which is:
	// - somewhat connectable to the original resource
	// - a valid k8s name
	// - idempotently calculatable
	// - unique for this combination of name/namespace/cluster

	namePrefix := strings.Join(names, "-")

	// use + as a separator since it can't be in an object name
	nameKey := strings.Join(names, "+")
	// it's possible that the suffix will be in the name, so we use hex to make it valid for k8s
	nameSuffix := hex.EncodeToString([]byte(nameKey))

	return controller.SafeConcatName(namePrefix, nameSuffix)
}
