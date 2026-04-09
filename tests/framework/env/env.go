package env

import (
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Config holds the envtest environment configuration.
type Config struct {
	CRDPaths []string
	Scheme   *runtime.Scheme
}

// NewEnvironment creates a new envtest.Environment with CRDs from the k3k charts directory.
// The scheme parameter should be created using the scheme package.
func NewEnvironment(scheme *runtime.Scheme) *envtest.Environment {
	return &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "charts", "k3k", "templates", "crds")},
		ErrorIfCRDPathMissing: true,
		Scheme:                scheme,
	}
}

// NewEnvironmentWithPaths creates a new envtest.Environment with custom CRD paths.
// This is useful when running tests from different locations in the directory tree.
func NewEnvironmentWithPaths(scheme *runtime.Scheme, crdPaths ...string) *envtest.Environment {
	return &envtest.Environment{
		CRDDirectoryPaths:     crdPaths,
		ErrorIfCRDPathMissing: true,
		Scheme:                scheme,
	}
}
