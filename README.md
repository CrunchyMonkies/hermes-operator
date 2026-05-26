# hermes-operator

A Kubernetes operator (Go) for deploying and managing
[Nous Research Hermes Agent](https://github.com/nousresearch/hermes-agent)
gateways declaratively via a `HermesAgent` Custom Resource.

> Full design: [`docs/specification.md`](docs/specification.md). Pinned upstream:
> Hermes Agent `v2026.5.16` (vendored at `third_party/hermes-agent`).

## What it does

One `HermesAgent` CR ⇒ one gateway = one `Deployment(Recreate, replicas≤1)` + **one
shared RWO PVC** (sub-paths for data, `~/.local`, and Homebrew) + ConfigMaps +
Service (+ optional Ingress) + an in-pod **reloader** sidecar. The gateway is a
singleton (SQLite + `gateway.lock`), so the operator enforces `replicas ∈ {0,1}`
and `Recreate` so the old pod releases the volume lock before the new one starts.

Highlights:
- Renders `config.yaml` from typed CRD fields + deep-merged `extraConfig`.
- Credentials via `Secret` refs — the operator never reads secret material
  (Secret `resourceVersion` feeds the config hash to trigger rollouts).
- **Single shared PVC** with a settable size.
- Declarative **skill activation** (defaults + custom + an auto-activated
  `package-management` skill).
- Declarative **package installation** via apt (boot-time, root) and Homebrew
  (persisted on the PVC, no sudo).
- Full **`podTemplate` overlay** strategic-merged over the operator's base pod,
  with operator invariants (shared-PVC mounts, config-hash, root-start)
  re-asserted afterward.
- Service + optional Ingress (verbatim annotations) per HTTP surface.
- Health-gated rollouts, finalizer-ordered teardown honoring `reclaimPolicy`.

## Components (images → `harbor.bne1.ouchi.com.au/applications/`)

| Component | Image | Build |
| --- | --- | --- |
| Operator | `hermes-operator` | `images/operator/Dockerfile` (distroless) |
| Agent | `hermes-agent` | `images/agent/Dockerfile` (upstream + Homebrew + skill + wrapper entrypoint) |
| Reloader | `hermes-reloader` | `images/reloader/Dockerfile` (`FROM` agent + Go binary) |

## Layout

```
api/v1alpha1/                 # CRD types + CEL validation + deepcopy
internal/config/              # config.yaml renderer + config hash
internal/resources/           # child-object builders (pvc/cm/svc/ingress/sa/deployment)
internal/controller/          # reconciler + finalizer
cmd/operator, cmd/reloader/   # the two binaries
images/{operator,agent,reloader}/
config/                       # kustomize (crd, rbac, manager, samples)
charts/hermes-operator/       # Helm chart (CRDs + RBAC + manager)
third_party/hermes-agent/     # upstream submodule @ v2026.5.16
```

## Develop

```bash
make generate manifests       # codegen + CRD/RBAC manifests
make build                    # build operator + reloader
make test                     # unit + envtest integration
make helm-lint helm-template  # render/validate the chart
kustomize build config/default

# Images (single-arch local, or buildx multi-arch + push to Harbor)
make docker-build
make docker-buildx IMG_REGISTRY=harbor.bne1.ouchi.com.au/applications VERSION=v0.1.0
make helm-push
```

## Scope

This repository implements the **v1alpha1 MVP** (specification §16/§17). The
`docker` (DinD) terminal backend, cron seeding, presets/channels controllers,
the `hermes-agent` Helm chart, and admission/conversion webhooks are modeled in
the API/validation but their runtime wiring is planned for v1beta1.
