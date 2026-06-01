# Toolchain image for building/testing hermes-operator without a host Go install.
#
# This is NOT a runtime image — it carries the Go toolchain + make + python3 so the
# repo's `make` targets (generate, manifests, test, build) run in a container. Mount
# the repo and run a target; generated files (deepcopy, CRD YAMLs) land back on the host.
#
# Build the image (context = repo root):
#   docker build -f images/build/Dockerfile -t hermes-operator-build .
#
# Run a make target with the repo mounted (caches speed up reruns):
#   docker run --rm -v "$PWD":/workspace -w /workspace \
#     -v hermes-gocache:/root/.cache/go-build -v hermes-gomod:/go/pkg/mod \
#     hermes-operator-build make test
#
# golang:1.26 satisfies go.mod's `go 1.25.7`; matches images/operator/Dockerfile.
FROM golang:1.26

# make: drives the Makefile. python3: hack/annotate-crds.py (helm-sync-crds).
# git: module version stamping. ca-certificates: tool/envtest downloads.
RUN apt-get update \
 && apt-get install -y --no-install-recommends make python3 git ca-certificates \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

# Use the image's Go as-is (>= go.mod's directive) instead of fetching a toolchain;
# disable VCS stamping since the mounted tree may not have a usable .git context.
ENV GOTOOLCHAIN=local \
    GOFLAGS=-buildvcs=false

# Default: the full verify pipeline (codegen + fmt + vet + envtest tests).
CMD ["make", "test"]
