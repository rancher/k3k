# Code conventions

- [Shell](#shell)
- [Go](#go)
- [Directory and file conventions](#directory-and-file-conventions)

## Shell

All scripts live in `scripts/` and are Bash scripts. The scripts should start them with `#!/bin/bash` and `set -eou pipefail`.
They only ever run under `make`, on a developer machine or a CI runner.

- Follow the [Google shell style guide](https://google.github.io/styleguide/shellguide.html).
- `make lint-shell` must pass. It runs [ShellCheck](https://www.shellcheck.net) at its default
  severity; install it from your package manager.

## Go

- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- [Effective Go](https://golang.org/doc/effective_go.html)
- Know and avoid [Go landmines](https://gist.github.com/lavalamp/4bd23295a9f32706a48f)
- Comment your code.
  - [Go's commenting conventions](http://blog.golang.org/godoc-documenting-go-code)
  - If reviewers ask questions about why the code is the way it is, that's a sign that comments might be helpful.
  - Every exported identifier needs a doc comment starting with its name, and every package needs a package comment on whichever file best represents it.
- Command-line flags should use dashes, not underscores
- Naming
  - Please consider package name when selecting an interface name, and avoid redundancy. For example, `storage.Interface` is better than `storage.StorageInterface`.
  - Do not use uppercase characters, underscores, or dashes in package names.
  - Avoid a package name that shadows a Go standard library package. For example, `pkg/logging` rather than `pkg/log`.
  - Initialisms keep their case. For example, `KubeAPIServerArg`, not `KubeApiServerArg`.
  - Please consider parent directory name when choosing a package name. For example, `pkg/controllers/autoscaler/foo.go` should say `package autoscaler` not `package autoscalercontroller`.
    - Unless there's a good reason, the `package foo` line should match the name of the directory in which the `.go` file exists.
    - Importers can use a different name if they need to disambiguate.
- Imports
  - Alias the common Kubernetes packages as `metav1`, `apierrors`, `corev1`, `appsv1` and `networkingv1`.
- Whitespace
  - Blank lines follow [wsl](https://github.com/bombsimon/wsl). Keep a statement next to the one it relates to, and separate a block from the code around it with a blank line.
- Linting
  - `make lint` must pass. Run `make lint-fix` first, it applies the formatting and wsl fixes.
  - The enabled rules live in `.golangci.yml`.

## Directory and file conventions

- Avoid general utility packages. Packages called "util" are suspect. Instead, derive a name that describes your desired function. For example, the utility functions dealing with waiting for operations are in the `wait` package and include functionality like `Poll`. The full name is `wait.Poll`.
- All filenames should be lowercase.
- Source file names should use underscores, not dashes.
- Package directories should generally avoid using separators as much as possible. When package names are multiple words, they usually should be in nested subdirectories.
- A directory that builds a binary is named after that binary. For example, `k3k-kubelet`.
