package main

import (
	"fmt"
	"os"
	"path"

	"github.com/spf13/cobra/doc"

	"github.com/rancher/k3k/cli/cmds"
)

func main() {
	// Instantiate the CLI application
	k3kcli := cmds.NewRootCmd()

	wd, err := os.Getwd()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	outputDir := path.Join(wd, "docs/cli")

	if err := doc.GenMarkdownTree(k3kcli, outputDir); err != nil {
		fmt.Println("Error generating documentation:", err)
		os.Exit(1)
	}

	fmt.Println("Documentation generated at " + outputDir)
}
