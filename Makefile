# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# kubectl kuberc is disabled by default for test isolation; enable with:
# - KUBECTL_KUBERC=true
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER ?= hermes-operator-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build operator and reloader binaries.
	go build -o bin/manager ./cmd/operator
	go build -o bin/reloader ./cmd/reloader

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/operator

##@ Containerized build (no host Go required)

# Toolchain image + a mounted-repo runner, so codegen/tests work without a host Go.
# Generated files (deepcopy, CRD YAMLs) are written back to the working tree.
BUILD_IMG       ?= hermes-operator-build
# Run as the host user with host-owned cache dirs so the container never leaves
# root-owned files in the working tree (bin/, generated files all stay writable).
BUILD_CACHE_DIR ?= $(HOME)/.cache/hermes-operator-build
DOCKER_RUN      ?= $(CONTAINER_TOOL) run --rm \
                   -u $(shell id -u):$(shell id -g) -e HOME=/tmp \
                   -v "$(CURDIR)":/workspace -w /workspace \
                   -v "$(BUILD_CACHE_DIR)/cache":/tmp/.cache \
                   -v "$(BUILD_CACHE_DIR)/go-pkg":/go/pkg \
                   $(BUILD_IMG)

.PHONY: build-image
build-image: ## Build the local toolchain image (images/build/Dockerfile).
	$(CONTAINER_TOOL) build -f images/build/Dockerfile -t $(BUILD_IMG) .

.PHONY: in-docker
in-docker: build-image ## Run a make target in the toolchain image, e.g. make in-docker TARGET="generate manifests".
	@mkdir -p "$(BUILD_CACHE_DIR)/cache" "$(BUILD_CACHE_DIR)/go-pkg"
	$(DOCKER_RUN) make $(TARGET)

##@ Images (Harbor)

# Registry and tags for the three published components (spec §9).
# RELEASE_VERSION is the single source of truth — the chart's appVersion. The
# image tag matches it with a leading "v" (release 0.1.0 -> image tag v0.1.0).
IMG_REGISTRY    ?= harbor.bne1.ouchi.com.au/applications
RELEASE_VERSION ?= $(shell sed -nE 's/^appVersion:[[:space:]]*"?([^"]+)"?.*/\1/p' charts/hermes-operator/Chart.yaml)
VERSION         ?= v$(RELEASE_VERSION)
UPSTREAM_TAG    ?= v2026.5.16
UPSTREAM_IMAGE  ?= nousresearch/hermes-agent:$(UPSTREAM_TAG)
PLATFORMS       ?= linux/amd64,linux/arm64

OPERATOR_IMG    ?= $(IMG_REGISTRY)/hermes-operator:$(VERSION)
AGENT_IMG       ?= $(IMG_REGISTRY)/hermes-agent:$(VERSION)
AGENT_IMG_PINNED?= $(IMG_REGISTRY)/hermes-agent:$(UPSTREAM_TAG)
RELOADER_IMG    ?= $(IMG_REGISTRY)/hermes-reloader:$(VERSION)

.PHONY: docker-build
docker-build: ## Build all three component images locally (single-arch).
	$(CONTAINER_TOOL) build -f images/operator/Dockerfile -t $(OPERATOR_IMG) .
	$(CONTAINER_TOOL) build -f images/agent/Dockerfile \
		--build-arg UPSTREAM_IMAGE=$(UPSTREAM_IMAGE) \
		-t $(AGENT_IMG) -t $(AGENT_IMG_PINNED) .
	$(CONTAINER_TOOL) build -f images/reloader/Dockerfile \
		--build-arg AGENT_IMAGE=$(AGENT_IMG) \
		-t $(RELOADER_IMG) .

.PHONY: docker-push
docker-push: ## Push all three component images.
	$(CONTAINER_TOOL) push $(OPERATOR_IMG)
	$(CONTAINER_TOOL) push $(AGENT_IMG)
	$(CONTAINER_TOOL) push $(AGENT_IMG_PINNED)
	$(CONTAINER_TOOL) push $(RELOADER_IMG)

.PHONY: docker-buildx
docker-buildx: ## Build and push all three images multi-arch ($(PLATFORMS)).
	- $(CONTAINER_TOOL) buildx create --name hermes-operator-builder
	$(CONTAINER_TOOL) buildx use hermes-operator-builder
	$(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) \
		-f images/operator/Dockerfile -t $(OPERATOR_IMG) .
	$(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) \
		-f images/agent/Dockerfile --build-arg UPSTREAM_IMAGE=$(UPSTREAM_IMAGE) \
		-t $(AGENT_IMG) -t $(AGENT_IMG_PINNED) .
	$(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) \
		-f images/reloader/Dockerfile --build-arg AGENT_IMAGE=$(AGENT_IMG) \
		-t $(RELOADER_IMG) .
	- $(CONTAINER_TOOL) buildx rm hermes-operator-builder

##@ Helm

HELM           ?= helm
HELM_OCI_REPO  ?= oci://harbor.bne1.ouchi.com.au/helm
CHART_DIR      ?= charts/hermes-operator

.PHONY: helm-sync-crds
helm-sync-crds: manifests ## Copy generated CRDs into the chart with the keep annotation.
	@mkdir -p $(CHART_DIR)/files/crds
	@python3 hack/annotate-crds.py config/crd/bases $(CHART_DIR)/files/crds

.PHONY: helm-lint
helm-lint: helm-sync-crds ## Lint the operator Helm chart.
	$(HELM) lint $(CHART_DIR)

.PHONY: helm-template
helm-template: ## Render the operator Helm chart for inspection.
	$(HELM) template hermes-operator $(CHART_DIR)

.PHONY: helm-package
helm-package: ## Package the operator Helm chart.
	mkdir -p dist
	$(HELM) package $(CHART_DIR) --destination dist

.PHONY: helm-push
helm-push: helm-package ## Push the packaged chart to the Harbor OCI registry.
	$(HELM) push dist/hermes-operator-*.tgz $(HELM_OCI_REPO)

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.20.1

#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.11.4
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		echo "Building custom golangci-lint with plugins..." && \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
