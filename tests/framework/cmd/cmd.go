// Package cmd provides a small helper to run external command-line tools
// (k3kcli, kubectl, helm, ...) from the test suites.
package cmd

import (
	"bytes"
	"context"
	"os/exec"
)

// RunCmd executes cmdName with args and returns its stdout, stderr and error.
func RunCmd(cmdName string, args ...string) (string, string, error) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}

	cmd := exec.CommandContext(context.Background(), cmdName, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}
