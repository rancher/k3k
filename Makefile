
REPO ?= rancher
VERSION ?= $(shell git describe --tags --always --dirty --match="v[0-9]*")

## Dependencies

GOLANGCI_LINT_VERSION := v1.63.4
CONTROLLER_TOOLS_VERSION ?= v0.14.0
GINKGO_VERSION ?= v2.21.0
ENVTEST_VERSION ?= latest
ENVTEST_K8S_VERSION := 1.31.0
CRD_REF_DOCS_VER ?= v0.1.0

GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)
GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo@$(GINKGO_VERSION)
CRD_REF_DOCS := go run github.com/elastic/crd-ref-docs@$(CRD_REF_DOCS_VER)

ENVTEST ?= go run sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)
ENVTEST_DIR ?= $(shell pwd)/.envtest
export KUBEBUILDER_ASSETS ?= $(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(ENVTEST_DIR) -p path)


.PHONY: all
all: version build-crds build package ## Run 'make' or 'make all' to run 'version', 'build-crds', 'build' and 'package'

.PHONY: version
version: ## Print the current version
	@echo $(VERSION)

.PHONY: build
build:	## Build the the K3k binaries (k3k, k3k-kubelet and k3kcli)
	@VERSION=$(VERSION) ./scripts/build

.PHONY: package
package: package-k3k package-k3k-kubelet	## Package the k3k and k3k-kubelet Docker images

.PHONY: package-%
package-%:
	docker build -f package/Dockerfile.$* \
		-t $(REPO)/$*:$(VERSION) \
		-t $(REPO)/$*:latest  \
		-t $(REPO)/$*:dev .

.PHONY: push
push: push-k3k push-k3k-kubelet		## Push the K3k images to the registry

.PHONY: push-%
push-%:
	docker push $(REPO)/$*:$(VERSION)
	docker push $(REPO)/$*:latest
	docker push $(REPO)/$*:dev


.PHONY: test
test:	## Run all the tests
	$(GINKGO) -v -r --label-filter=$(label-filter)

.PHONY: test-unit
test-unit:	## Run the unit tests (skips the e2e)
	$(GINKGO) -v -r --skip-file=tests/*

.PHONY: test-controller
test-controller:	## Run the controller tests (pkg/controller)
	$(GINKGO) -v -r pkg/controller

.PHONY: test-e2e
test-e2e:	## Run the e2e tests
	$(GINKGO) -v -r tests

.PHONY: build-crds
build-crds:	## Build the CRDs specs
	@# This will return non-zero until all of our objects in ./pkg/apis can generate valid crds.
	@# allowDangerousTypes is needed for struct that use floats
	$(CONTROLLER_GEN) crd:generateEmbeddedObjectMeta=true,allowDangerousTypes=false \
		paths=./pkg/apis/... \
		output:crd:dir=./charts/k3k/crds

.PHONY: docs
docs:	## Build the CRDs and CLI docs
	$(CRD_REF_DOCS) --config=./docs/crds/config.yaml --renderer=markdown --source-path=./pkg/apis/k3k.io/v1alpha1 --output-path=./docs/crds/crd-docs.md
	@go run ./docs/cli/genclidoc.go

.PHONY: lint
lint:	## Find any linting issues in the project
	$(GOLANGCI_LINT) run --timeout=5m

.PHONY: validate
validate: build-crds docs ## Validate the project checking for any dependency or doc mismatch
	$(GINKGO) unfocus
	go mod tidy
	git status --porcelain
	git --no-pager diff --exit-code

.PHONY: install
install:	## Install K3k with Helm on the targeted Kubernetes cluster
	helm upgrade --install --namespace k3k-system --create-namespace \
		--set image.repository=$(REPO)/k3k \
		--set image.tag=$(VERSION) \
		--set sharedAgent.image.repository=$(REPO)/k3k-kubelet \
		--set sharedAgent.image.tag=$(VERSION) \
		k3k ./charts/k3k/

.PHONY: help
help:	## Show this help.
	@egrep -h '\s##\s' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m  %-30s\033[0m %s\n", $$1, $$2}'