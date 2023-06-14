#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

CODEGEN_GIT_PKG=https://github.com/kubernetes/code-generator.git
git clone --depth 1 ${CODEGEN_GIT_PKG} || true

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=./code-generator

"${CODEGEN_PKG}/generate-groups.sh" \
 "deepcopy" \
 github.com/rancher/k3k/pkg/generated \
 github.com/rancher/k3k/pkg/apis \
 "k3k.io:v1alpha1" \
 --go-header-file "${SCRIPT_ROOT}"/hack/boilerplate.go.txt \
 --output-base "$(dirname "${BASH_SOURCE[0]}")/../../../.."

rm -rf code-generator
