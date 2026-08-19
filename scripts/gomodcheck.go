//go:build ignore

// gomodcheck compares the require sets of two go.mod files and reports every
// module required by both at a different version.
//
// Usage:
//
//	go run scripts/gomodcheck.go go.mod pkg/apis/go.mod
//
// Exit codes: 0 in sync, 1 version mismatch, 2 usage/read/parse error.
package main

import (
	"fmt"
	"os"
	"sort"

	"golang.org/x/mod/modfile"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: go run scripts/gomodcheck.go <go.mod> <go.mod>\n")
		os.Exit(2)
	}

	pathA, pathB := os.Args[1], os.Args[2]

	reqA, err := requires(pathA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gomodcheck: %v\n", err)
		os.Exit(2)
	}

	reqB, err := requires(pathB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gomodcheck: %v\n", err)
		os.Exit(2)
	}

	var common, mismatched []string

	for mod, verA := range reqA {
		verB, ok := reqB[mod]
		if !ok {
			continue
		}

		common = append(common, mod)

		if verA != verB {
			mismatched = append(mismatched, mod)
		}
	}

	sort.Strings(mismatched)

	if len(mismatched) == 0 {
		fmt.Printf("all %d common modules match\n", len(common))
		return
	}

	pad := max(len(pathA), len(pathB))

	for _, mod := range mismatched {
		fmt.Printf("MISMATCH  %s\n", mod)
		fmt.Printf("  %-*s  %s\n", pad, pathA, reqA[mod])
		fmt.Printf("  %-*s  %s\n\n", pad, pathB, reqB[mod])
	}

	fmt.Printf("%s across %d common modules\n", plural(len(mismatched), "mismatch", "mismatches"), len(common))
	os.Exit(1)
}

// requires parses the go.mod file at path and returns its require directives,
// both direct and indirect, as a module path to version map.
func requires(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	file, err := modfile.Parse(path, data, nil)
	if err != nil {
		return nil, err
	}

	reqs := make(map[string]string, len(file.Require))
	for _, req := range file.Require {
		reqs[req.Mod.Path] = req.Mod.Version
	}

	return reqs, nil
}

func plural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}

	return fmt.Sprintf("%d %s", n, plural)
}
