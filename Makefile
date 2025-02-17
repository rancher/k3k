
REPO ?= rancher
VERSION ?= $(shell git describe --tags --always --dirty --match="v[0-9]*")

.PHONY: all
all: version build package

# Output the current version
.PHONY: version
version:
	@echo $(VERSION)

.PHONY: build
build:
	@VERSION=$(VERSION) ./scripts/build

# Package Docker images
.PHONY: package
package: package-k3k package-k3k-kubelet

.PHONY: package-%
package-%:
	docker build -f package/Dockerfile.$* \
		-t $(REPO)/$*:$(VERSION) \
		-t $(REPO)/$*:latest  \
		-t $(REPO)/$*:dev .

# Push images to the registry
.PHONY: push
push: push-k3k push-k3k-kubelet

.PHONY: push-%
push-%:
	docker push $(REPO)/$*:$(VERSION)
	docker push $(REPO)/$*:latest
	docker push $(REPO)/$*:dev

.PHONY: build-crds
build-crds:
	@# This will return non-zero until all of our objects in ./pkg/apis can generate valid crds.
	@# allowDangerousTypes is needed for struct that use floats
	$(CONTROLLER_GEN) crd:generateEmbeddedObjectMeta=true,allowDangerousTypes=false \
		paths=./pkg/apis/... \
		output:crd:dir=./charts/k3k/crds

.PHONY: test
test:
	$(GINKGO) -v -r --skip-file=tests/*

.PHONY: lint
lint:
	$(GOLANGCI_LINT) run --timeout=5m


##@ Dependencies

GOLANGCI_LINT_VERSION := v1.63.4
CONTROLLER_TOOLS_VERSION ?= v0.14.0
GINKGO_VERSION ?= v2.21.0
ENVTEST_VERSION ?= release-0.18

GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)
GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo@$(GINKGO_VERSION)
ENVTEST ?= go run sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)
ENVTEST_DIR ?= $(shell pwd)/.envtest
