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

## Upgrading the pinned Hermes version

The operator pins one upstream **Hermes Agent** version (git submodule at
`third_party/hermes-agent`) and models a *curated subset* of its `config.yaml` schema in
the CRDs; everything not modelled first-class is reachable via
`spec.defaultProfile.extraConfig` (deep-merged). When asked to "upgrade hermes", follow
this runbook — it ends with regeneration and tests, so it is safe to repeat.

1. **Pick the target tag.** Fetch and list upstream releases:
   ```bash
   git -C third_party/hermes-agent fetch --tags
   git -C third_party/hermes-agent tag --sort=-creatordate | head
   ```
2. **Review the schema delta first** (do this before touching the operator). Diff the
   upstream files that drive the operator's mirrored surface, between the current pin and
   the target tag (`OLD` = current, e.g. `v2026.5.29.2`; `NEW` = target):
   ```bash
   cd third_party/hermes-agent
   git diff OLD NEW -- cli-config.yaml.example          # human-facing schema doc
   git diff OLD NEW -- hermes_cli/config.py             # DEFAULT_CONFIG dict + _config_version
   git diff OLD NEW -- tools/lazy_deps.py               # mirrored pip pins (lazydeps.go)
   git diff OLD NEW -- gateway/platforms/webhook.py     # webhook contract (webhook.go)
   ```
   The authoritative key-by-key delta is the `DEFAULT_CONFIG` dict diff. Note: the new
   `_config_version`; every **added** key (decide first-class CRD field vs. leave to
   `extraConfig`); every **removed/renamed** key (must drop it from the operator only if
   the operator actually renders it); and whether any mirrored pip pin moved.
3. **Bump the submodule and every version anchor.** Find them with `git grep vOLD` (and the
   bare `OLD` form for `Chart.yaml`'s `appVersion`, which has **no** `v` prefix). The set:
   - pins — `Makefile` (`UPSTREAM_TAG`), `images/agent/Dockerfile`, `images/reloader/Dockerfile`,
     `charts/hermes-operator/Chart.yaml` (`appVersion`), `config/samples/…`, `README.md`,
     `docs/specification.md` (spec line 1 also carries the release + commit short hash);
   - "verified at tag …" comment anchors — `internal/config/render.go`,
     `internal/resources/{names,lazydeps,webhook}.go`, `api/v1alpha1/hermesagent_subtypes.go`.
   Then `git -C third_party/hermes-agent checkout NEW && git add third_party/hermes-agent`.
   (The chart's own `version:` — packaging, e.g. `2026.5.29+5` — is bumped by the release
   flow, not the hermes upgrade.)
4. **Bump the rendered schema version.** Set `ConfigVersion` in `internal/config/render.go`
   to the new `_config_version`. It feeds `_config_version` into every rendered `config.yaml`.
5. **Reconcile the curated schema** against step 2's delta:
   - first-class new keys → add a CRD field (`api/v1alpha1/hermesagent_subtypes.go`; profile-
     level config keys hang off `ProfileConfig`, pod-level surfaces like the dashboard off
     `HermesAgentSpec`) **and** wire it: render into `config.yaml` (`internal/config/render.go`,
     reuse the `put*` helpers) or inject env (`internal/resources/pod_parts.go`). Keep secret
     material in Secrets via `SecretKeyRef`, never the CRD or `config.yaml`;
   - removed keys the operator rendered → delete the field + its render/env wiring;
   - re-verify mirrored constants still match upstream: `lazydeps.go` pip pins, `names.go`
     paths/ports, `webhook.go` platform/secret env names;
   - add/extend tests in `internal/config/render_test.go` and
     `internal/resources/integrations_test.go`.
6. **Regenerate and verify** (see "After changing the API" above):
   ```bash
   make in-docker TARGET="generate manifests helm-sync-crds"
   git status --porcelain          # expect only intended changes (incl. regenerated CRDs)
   make in-docker TARGET=test
   make in-docker TARGET=build
   ```
   Confirm a rendered `config.yaml` carries the new `_config_version` and any new blocks, and
   `make docker-build` builds the agent/reloader images `FROM …hermes-agent:NEW`.

## Production images (unchanged)

`make docker-build` / `docker-buildx` build the three published components
(`images/operator`, `images/agent`, `images/reloader`). The toolchain image above is
separate and is never pushed.
