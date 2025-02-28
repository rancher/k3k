package main

import (
	"bytes"
	"fmt"
	"os"
	"path"

	"github.com/rancher/k3k/cli/cmds"
)

func main() {
	// Instantiate the CLI application
	app := cmds.NewApp()

	// Generate the Markdown documentation
	md, err := app.ToMarkdown()
	if err != nil {
		fmt.Println("Error generating documentation:", err)
		os.Exit(1)
	}

	wd, err := os.Getwd()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	outputFile := path.Join(wd, "docs/cli/cli-docs.md")

	// Check if the file exists and if the content is different
	existingContent, err := os.ReadFile(outputFile)
	if err == nil && bytes.Equal(existingContent, []byte(md)) {
		fmt.Println("Documentation is up to date, no changes made.")
		return
	}

	err = os.WriteFile(outputFile, []byte(md), 0644)
	if err != nil {
		fmt.Println("Error generating documentation:", err)
		os.Exit(1)
	}

	fmt.Println("Documentation generated at " + outputFile)
}
