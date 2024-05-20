#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

set -x
CODEGEN_GIT_PKG=https://github.com/kubernetes/code-generator.git
git clone --depth 1 ${CODEGEN_GIT_PKG} || true

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=./code-generator

source ${CODEGEN_PKG}/kube_codegen.sh

kube::codegen::gen_helpers \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    "${SCRIPT_ROOT}/pkg/apis"

rm -rf code-generator
