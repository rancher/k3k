package client

import (
	"k8s.io/apimachinery/pkg/runtime"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// NewScheme creates a new Kubernetes runtime scheme with core APIs and k3k CRDs.
// This is suitable for most k3k test scenarios including integration and E2E tests.
func NewScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	// Add core Kubernetes scheme (includes most common types)
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}

	// Add k3k CRDs
	if err := v1beta1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	return scheme
}
