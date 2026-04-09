package container

import (
	"context"
	"os"
	"strings"

	"github.com/testcontainers/testcontainers-go/modules/k3s"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// SetupK3s creates and starts a K3s testcontainer with the specified version and loads the provided images.
// It returns the container instance and a temporary kubeconfig file path.
// The kubeconfig is automatically cleaned up via DeferCleanup.
func SetupK3s(ctx context.Context, k3sVersion, controllerImage, kubeletImage string) (*k3s.K3sContainer, string) {
	GinkgoHelper()

	var (
		err        error
		kubeconfig []byte
	)

	// Normalize version string (replace + with -)
	k3sVersion = strings.ReplaceAll(k3sVersion, "+", "-")

	// Start K3s container
	k3sContainer, err := k3s.Run(ctx, "rancher/k3s:"+k3sVersion)
	Expect(err).To(Not(HaveOccurred()))

	containerIP, err := k3sContainer.ContainerIP(ctx)
	Expect(err).To(Not(HaveOccurred()))

	GinkgoWriter.Println("K3s containerIP: " + containerIP)

	// Get kubeconfig from container
	kubeconfig, err = k3sContainer.GetKubeConfig(ctx)
	Expect(err).To(Not(HaveOccurred()))

	// Write kubeconfig to temp file
	tmpFile, err := os.CreateTemp("", "kubeconfig-")
	Expect(err).To(Not(HaveOccurred()))

	_, err = tmpFile.Write(kubeconfig)
	Expect(err).To(Not(HaveOccurred()))
	Expect(tmpFile.Close()).To(Succeed())

	kubeconfigPath := tmpFile.Name()

	// Load images into the K3s container
	err = k3sContainer.LoadImages(ctx, controllerImage+":dev", kubeletImage+":dev")
	Expect(err).To(Not(HaveOccurred()))

	// Register cleanup
	DeferCleanup(os.Remove, kubeconfigPath)

	// Set KUBECONFIG environment variable
	Expect(os.Setenv("KUBECONFIG", kubeconfigPath)).To(Succeed())
	GinkgoWriter.Printf("KUBECONFIG set to: %s\n", kubeconfigPath)

	return k3sContainer, kubeconfigPath
}
