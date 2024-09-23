#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

set -x
CODEGEN_GIT_PKG=https://github.com/kubernetes/code-generator.git
git clone --depth 1 ${CODEGEN_GIT_PKG} || true

K8S_VERSION=$(cat go.mod | grep "=> k8s.io/apiserver" | rev | cut -d " " -f 1 | rev)
SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=./code-generator

# cd into the git dir to checkout the code gen version compatible with the k8s version that this is using
cd $CODEGEN_PKG
git fetch origin tag ${K8S_VERSION}
git checkout ${K8S_VERSION}
cd -

source ${CODEGEN_PKG}/kube_codegen.sh

kube::codegen::gen_helpers \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    --input-pkg-root "${SCRIPT_ROOT}/pkg/apis" \
    --output-base "${SCRIPT_ROOT}/pkg/apis"

rm -rf code-generator
