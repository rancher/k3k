package k3k_test

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"k8s.io/apimachinery/pkg/util/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func K3kcli(args ...string) (string, string, error) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}

	cmd := exec.CommandContext(context.Background(), "k3kcli", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

var _ = When("using the k3kcli", Label("cli"), func() {
	It("can get the version", func() {
		stdout, _, err := K3kcli("--version")
		Expect(err).To(Not(HaveOccurred()))
		Expect(stdout).To(ContainSubstring("k3kcli Version: v"))
	})

	When("trying the cluster commands", func() {
		It("can create, list and delete a cluster", func() {
			var (
				stdout string
				stderr string
				err    error
			)

			clusterName := "cluster-" + rand.String(5)
			clusterNamespace := "k3k-" + clusterName

			_, stderr, err = K3kcli("cluster", "create", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring("You can start using the cluster"))

			stdout, stderr, err = K3kcli("cluster", "list")
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(BeEmpty())
			Expect(stdout).To(ContainSubstring(clusterNamespace))

			_, stderr, err = K3kcli("cluster", "delete", clusterName)
			Expect(err).To(Not(HaveOccurred()), string(stderr))
			Expect(stderr).To(ContainSubstring(fmt.Sprintf("Deleting [%s] cluster in namespace [%s]", clusterName, clusterNamespace)))
		})
	})
})
