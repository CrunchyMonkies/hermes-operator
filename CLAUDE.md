# CLAUDE.md

Project guidance for Claude Code. See `AGENTS.md` for the full kubebuilder/repo guide
(layout, generated files, conventions); this file covers how to **build** here.

## Building without a host Go install

This repo targets Go 1.25.7 (`go.mod`) and is meant to build inside a Go container
(`.devcontainer` uses `golang:1.25`). Some hosts have **no `go` on PATH**, so `make
generate/manifests/test` fail with `go: command not found`. Use the containerized
toolchain instead of installing Go ad hoc.

**Toolchain image:** `images/build/Dockerfile` — `golang:1.26` + `make` + `python3` +
`git`. It is a build/dev image (not a runtime image). The repo is mounted at run time so
regenerated files (`zz_generated.deepcopy.go`, CRD YAMLs under `config/crd/bases` and
`charts/.../files/crds`) are written back to the working tree.

```bash
# Build the toolchain image once (context = repo root):
make build-image

# Run any make target inside it (repo mounted, caches persisted across runs):
make in-docker TARGET="generate manifests helm-sync-crds"   # codegen
make in-docker TARGET=test                                  # fmt + vet + envtest tests
make in-docker TARGET=build                                 # compile binaries -> bin/
```

`make in-docker` wraps `docker run` with the repo mounted at `/workspace`, running as the
host user (`-u $(id -u):$(id -g)`) so it never leaves root-owned files in the tree. The Go
build/module caches persist on the host under `$HOME/.cache/hermes-operator-build`
(override with `BUILD_CACHE_DIR`). The tools (`controller-gen`, `setup-envtest`,
`golangci-lint`) install into `./bin` on first run; envtest auto-provisions its k8s
binaries.

The image's Go is used as-is via `GOTOOLCHAIN=local` (1.26 ≥ go.mod's 1.25.7), so no
extra toolchain is downloaded.

## After changing the API (`api/v1alpha1/*_types.go` or markers)

Always regenerate, then verify there's no drift:
```bash
make in-docker TARGET="generate manifests helm-sync-crds"
git status --porcelain   # expect only your intended changes
make in-docker TARGET=test
```

## Production images (unchanged)

`make docker-build` / `docker-buildx` build the three published components
(`images/operator`, `images/agent`, `images/reloader`). The toolchain image above is
separate and is never pushed.
