package client

import (
	"k8s.io/apimachinery/pkg/runtime"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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

// NewPolicyScheme creates a new Kubernetes runtime scheme with core APIs, networking APIs,
// and k3k CRDs. This is required for policy controller tests that need apps/v1 and networking/v1.
func NewPolicyScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	// Add core API types
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	// Add apps API types
	if err := appsv1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	// Add networking API types
	if err := networkingv1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	// Add k3k CRDs
	if err := v1beta1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	return scheme
}
