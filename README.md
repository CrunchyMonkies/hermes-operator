# hermes-operator

A Kubernetes operator (Go) for deploying and managing
[Nous Research Hermes Agent](https://github.com/nousresearch/hermes-agent)
gateways declaratively via a `HermesAgent` Custom Resource.

> Full design: [`docs/specification.md`](docs/specification.md). Pinned upstream:
> Hermes Agent `v2026.5.16` (vendored at `third_party/hermes-agent`).

## What it does

One `HermesAgent` CR ⇒ one gateway = one `Deployment(Recreate, replicas≤1)` + **one
shared RWO PVC** (sub-paths for data, `~/.local`, Homebrew, and the dind store) +
ConfigMaps + Service (+ optional Ingress) + an in-pod **reloader** sidecar. The
gateway is a singleton (SQLite + `gateway.lock`), so the operator enforces
`replicas ∈ {0,1}` and `Recreate` so the old pod releases the volume lock before
the new one starts.

Highlights:
- Renders `config.yaml` from typed CRD fields + deep-merged `extraConfig`.
- Credentials via `Secret` refs — the operator never reads secret material
  (a Secret's `resourceVersion` feeds the config hash to trigger rollouts).
- **Single shared PVC** with a settable size.
- **Model providers** — typed `model.providers[]` for hermes built-ins
  (anthropic/openai/xai/openrouter/…, selected by name/alias) and custom
  OpenAI-compatible endpoints (with per-model context windows + key injection).
- **MCP servers** — typed `mcp.servers[]` (stdio + http/sse) with per-server tool
  filtering and Secret-backed credentials injected for `${VAR}` interpolation.
- **Pre-install of optional deps** onto the PVC (pip SDKs for channels/backends,
  `honcho-ai`, Apptainer for singularity) so they survive restarts.
- Declarative **skill activation** and **package installation** — pip (init
  container, persisted on the PVC via `PYTHONPATH`) + Homebrew (no sudo).
- Operator-managed **Docker-in-Docker** sidecar for the `docker` terminal backend,
  and an in-cluster **kubeconfig** written to `~/.kube/config`.
- A single shared **Ingress** config defaulted across every HTTP surface, with
  auto webhook ingress for webhook-capable channels.
- Full **`podTemplate` overlay** strategic-merged over the operator's base pod,
  with operator invariants (shared-PVC mounts, config-hash, root-start) re-asserted.
- Health-gated rollouts, finalizer-ordered teardown honoring `reclaimPolicy`.

## Components (images → `harbor.bne1.ouchi.com.au/applications/`)

| Component | Image | Build |
| --- | --- | --- |
| Operator | `hermes-operator` | `images/operator/Dockerfile` (distroless) |
| Agent | `hermes-agent` | `images/agent/Dockerfile` (upstream + Homebrew + skill + wrapper entrypoint) |
| Reloader | `hermes-reloader` | `images/reloader/Dockerfile` (`FROM` agent + Go binary) |

---

# HermesAgent reference

`apiVersion: hermes.nousresearch.io/v1alpha1`, `kind: HermesAgent`. Everything below
is under `.spec`. Optional fields show their default; `—` means no default (unset).
Config-rendered fields note their `config.yaml` target as `→ key`.

### Top level

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `image` | string | **required** | Agent container image (the operator's derived image). |
| `imagePullPolicy` | string | `IfNotPresent` | |
| `imagePullSecrets` | []LocalObjectReference | — | Pull secrets (name refs). |
| `replicas` | int (0–1) | `1` | Singleton; `0` pauses (scales the Deployment to 0). |
| `presetRef.name` | string | — | A `HermesConfigPreset` deep-merged under this spec (CR wins). |
| `soul` | string | — | Persona rendered to `/opt/data/SOUL.md`. |
| `env` | []corev1.EnvVar | — | Extra agent-container env (appended after operator vars). |
| `envFrom` | []corev1.EnvFromSource | — | Mirrored onto the agent container. |
| `authJSONBootstrapSecretRef` | SecretKeyRef | — | Seeds `auth.json` once on first boot. |
| `extraConfig` | object (free-form) | — | Deep-merged into `config.yaml` (preserved verbatim). |
| `extraConfigPrecedence` | `merge`\|`override` | `merge` | `merge`: typed fields win; `override`: extraConfig wins. |
| `resources` | corev1.ResourceRequirements | — | Agent container resources. |
| `shmSize` | Quantity | — | `/dev/shm` emptyDir size (browser tools). |
| `hermesUID` / `hermesGID` / `fsGroup` | int64 | `10000` | Run-as ids / fsGroup. |
| `runAsRoot` | bool | `true` | Start as root so the entrypoint can `usermod`/`gosu` then drop to hermes. |
| `podTemplate` | corev1.PodTemplateSpec | — | Strategic-merge overlay; operator invariants re-asserted after. |

### `model` (→ `model:`)

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `model.default` | string | — | Default model id, e.g. `claude-opus-4-7`. → `model.default` |
| `model.provider` | string | `auto` | Routing mode, **or** a `providers[]` entry name to make it active. → `model.provider` |
| `model.baseURL` | string | — | Endpoint override; defaults from the selected custom provider. → `model.base_url` |
| `model.apiMode` | string | — | Wire protocol: `chat_completions`, `anthropic_messages`, `codex_responses`, `bedrock_converse`. → `model.api_mode` |
| `model.contextLength` | int64 | — | Global context window. → `model.context_length` |
| `model.maxTokens` | int64 | — | Output cap. → `model.max_tokens` |
| `model.providers[]` | []ProviderSpec | — | Built-in + custom providers (see below). Custom entries → `custom_providers:`. |

**`model.providers[]` — ProviderSpec**

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `name` | string | **required** | Built-in name/alias (`anthropic`,`claude`,`openai`,`grok`,`xai`,`openrouter`,`zai`,`kimi`,`minimax`,`deepseek`,…) or a custom name. |
| `baseURL` | string | — | Set ⇒ custom OpenAI-compatible endpoint; omit ⇒ built-in (hermes knows the URL). |
| `apiMode` | string | — | As above. |
| `keyEnv` | string | — | Override the env var the key is injected under (else built-in's known var, or `<NAME>_API_KEY` for custom). |
| `keySecretRef` | SecretKeyRef | — | API key the operator injects as an env var. Omit for OAuth providers (`hermes login`) or keyless endpoints. |
| `models[]` | []ProviderModelSpec | — | `{ name, contextLength }` — per-model context window (custom providers). |

Selecting `model.provider` = a built-in alias resolves the key env automatically
(`claude`→`ANTHROPIC_API_KEY`, `grok`→`XAI_API_KEY`, `openai`→`OPENROUTER_API_KEY`, …);
for a custom active provider the operator derives `model.base_url`/`api_mode` from it.

### `agent` (→ `agent:`)

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `agent.maxTurns` | int32 | — | → `agent.max_turns` |
| `agent.reasoningEffort` | `xhigh`\|`high`\|`medium`\|`low`\|`minimal`\|`none` | — | → `agent.reasoning_effort` |
| `agent.disabledToolsets` | []string | — | → `agent.disabled_toolsets` |

### `compression` (→ `compression:`, hot-reloadable) · `memory` (→ `memory:`)

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `compression.enabled` | *bool | — | |
| `compression.threshold` | string (number) | — | e.g. `"0.5"` |
| `compression.targetRatio` | string (number) | — | e.g. `"0.2"` |
| `compression.protectLastN` / `protectFirstN` | *int32 | — | |
| `memory.memoryEnabled` | *bool | — | → `memory.memory_enabled` |
| `memory.userProfileEnabled` | *bool | — | → `memory.user_profile_enabled` |

### `searxng` · `honcho`

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `searxng.url` | string | — | → `SEARXNG_URL` env. |
| `honcho.baseURL` | string | — | → `HONCHO_BASE_URL`. Honcho is "in use" when this or apiKeySecretRef is set. |
| `honcho.apiKeySecretRef` | SecretKeyRef | — | → `HONCHO_API_KEY` (hosted Honcho). |
| `honcho.installPackage` | *bool | `true` | Pre-install `honcho-ai` onto the PVC (via the pip-install init). |

### `mcp` (→ `mcp_servers:`)

`mcp.servers[]` declares Model Context Protocol servers, rendered to config.yaml
`mcp_servers` keyed by `name`. A server is **stdio** (set `command`) or **http/sse**
(set `url`) — exactly one. Credentials go in `secretEnv` (a Secret key injected as a
container env var) and are referenced with `${ENVNAME}` inside `headers`/`env` values
(hermes interpolates them at connect time; values pass through the operator untouched).

**`mcp.servers[]` — MCPServerSpec**

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `name` | string | **required** | Keys the server in `mcp_servers` (unique). |
| `enabled` | *bool | `true` | Toggle the server. |
| `transport` | `stdio`\|`http`\|`sse` | — | `stdio` implied by `command`; `url` implies `http` unless set to `sse`. |
| `command` | string | — | stdio: subprocess to launch. **Exactly one of `command`/`url`.** |
| `args` | []string | — | stdio: command args. |
| `env` | map[string]string | — | stdio: subprocess env; values may use `${VAR}`. |
| `url` | string | — | http/sse: endpoint. **Exactly one of `command`/`url`.** |
| `headers` | map[string]string | — | http/sse: request headers; values may use `${VAR}` (e.g. `Bearer ${TOK}`). |
| `sslVerify` | *bool | `true` | http/sse: TLS verification. |
| `timeoutSeconds` | *int32 | `120` | Per tool-call timeout. |
| `connectTimeoutSeconds` | *int32 | `60` | Initial connection timeout. |
| `supportsParallelToolCalls` | *bool | `false` | stdio: allow concurrent tool calls. |
| `tools.include` / `tools.exclude` | []string | — | Filter exposed tools (set at most one). |
| `secretEnv[]` | []{`name`,`secretRef`} | — | Inject a Secret key as env var `name`; reference via `${name}`. |
| `extraConfig` | object | — | Deep-merged into this server (e.g. `oauth`, `sampling`); typed fields win. |

### `ingress` (shared, → Ingress objects)

Top-level `spec.ingress` is the shared Ingress config. When enabled (and
`dashboard.enabled`), the dashboard is exposed at `ingress.path`. Its
`host`/`className`/`annotations`/`tls`/`pathType` are inherited by `apiServer` and
channel-webhook ingresses unless they set their own.

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `ingress.enabled` | bool | `false` | |
| `ingress.host` | string | — | Base host; required (here or per-surface) when an ingress is enabled. |
| `ingress.className` | string | — | → `spec.ingressClassName`. |
| `ingress.path` | string | `/` | |
| `ingress.pathType` | `Exact`\|`Prefix`\|`ImplementationSpecific` | `Prefix` | |
| `ingress.annotations` | map[string]string | — | Merged verbatim (nginx/cert-manager/etc.). |
| `ingress.tls[]` | []{`hosts[]`,`secretName`} | — | TLS blocks. |

### `apiServer` (→ env, port 8642) · `dashboard` (→ env, port 9119)

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `apiServer.enabled` | bool | `false` | Requires `keySecretRef` + a non-localhost `host`. |
| `apiServer.host` | string | `0.0.0.0` | |
| `apiServer.port` | int32 | `8642` | |
| `apiServer.keySecretRef` | SecretKeyRef | — | → `API_SERVER_KEY`. |
| `apiServer.corsOrigins` | []string | — | |
| `apiServer.service` | ServiceSpec | — | Service type/annotations for this surface. |
| `apiServer.ingress` | IngressSpec | — | Per-surface ingress (inherits `spec.ingress`). |
| `dashboard.enabled` | bool | `false` | |
| `dashboard.host` | string | `0.0.0.0` | |
| `dashboard.port` | int32 | `9119` | |
| `dashboard.service` | ServiceSpec | — | (Dashboard ingress is the shared `spec.ingress`.) |

### `channels[]` (→ Service ports / Ingress / env)

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `type` | `telegram`\|`discord`\|`slack`\|`whatsapp`\|`signal`\|`email`\|`teams`\|`google_chat` | **required** | |
| `enabled` | bool | `true` | |
| `secretRef` | LocalObjectReference | — | Secret whose keys are the platform creds (injected via `envFrom`). |
| `webhookPort` | int32 | `0` | `0` = polling; `>0` (or auto for webhook-capable channels) exposes a Service port. |
| `ingress` | IngressSpec | — | Opt into a webhook ingress (auto path `/webhooks/<type>`; only telegram serves an inbound webhook today). |
| `installDeps` | *bool | `true` | Pre-install the platform's pip deps (telegram/discord/slack). |

### `skills` (→ `skills:`)

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `skills.disabled` | []string | — | Disable bundled skills. |
| `skills.platformDisabled` | map[string][]string | — | Per-platform disables. |
| `skills.externalDirs` | []string | — | Read-only external skill dirs. |
| `skills.creationNudgeInterval` | int32 | `15` | |
| `skills.enablePackageManagementSkill` | *bool | `true` | Ensure the built-in package-management skill. |
| `skills.custom[]` | []CustomSkill | — | `{ name, enabled=true, ` **one of** ` sourceRef{configMapName\|secretName} \| inline }`. |

### `packages` (→ init container + reloader)

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `packages.pip` | []string | — | Installed to the PVC user-site (init container), loaded via `PYTHONPATH`. |
| `packages.pipImage` | string | `python:3.13-slim` | Must match the agent's Python (3.13 at the pinned tag). |
| `packages.brew` | []string | — | Homebrew packages on the PVC (no sudo). |
| `packages.homebrewPrefix` | string | `/home/linuxbrew/.linuxbrew` | |

### `runtime` (→ `terminal:`/`code_execution:`/`delegation:`)

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `runtime.terminalBackend` | `local`\|`docker`\|`ssh`\|`modal`\|`daytona`\|`vercel_sandbox`\|`singularity` | `local` | → `terminal.backend` |
| `runtime.terminalTimeout` | int32 | — | → `terminal.timeout` |
| `runtime.installDeps` | *bool | `true` | Pre-install the backend's deps (modal/daytona/vercel SDKs, or Apptainer). |
| `runtime.codeExecution.timeout` / `.maxToolCalls` | int32 | — | → `code_execution.*` |
| `runtime.delegation.maxIterations` / `.maxConcurrentChildren` | int32 | — | → `delegation.*` |
| `runtime.docker.image` | string | `docker:27-dind` | DinD sidecar image. |
| `runtime.docker.rootless` | bool | `false` | Selects the `-dind-rootless` image when image is default. |
| `runtime.docker.socketTransport` | `unix`\|`tcp` | `unix` | |
| `runtime.docker.mountCwdToWorkspace` | bool | `false` | → `terminal.docker_mount_cwd_to_workspace` |
| `runtime.docker.resources` | ResourceRequirements | — | DinD sidecar resources. |
| `runtime.docker.storage.size` | Quantity | — | dind image store (subPath on the shared PVC). |
| `runtime.singularity.installImage` | string | `rockylinux:9` | Image for the Apptainer installer (needs curl/rpm2cpio/cpio). |

### `cron` (→ `/opt/data/cron/jobs.json`)

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `cron.pruneUnmanaged` | bool | `false` | Make the declared set authoritative. |
| `cron.jobs[]` | []{`name`,`schedule`,`prompt`} | — | Seeded scheduled tasks. |

### `storage` · `serviceAccount` · `probes` · `kubeconfig`

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `storage.size` | Quantity | — | Shared PVC size. Mutually exclusive with `existingClaim`. |
| `storage.storageClassName` | string | — | |
| `storage.existingClaim` | string | — | Use a pre-created PVC instead of provisioning. |
| `storage.reclaimPolicy` | `Retain`\|`Delete` | `Retain` | PVC fate on CR deletion. |
| `serviceAccount.create` | bool | `true` | Provision a per-agent SA + bind the reloader Role. |
| `serviceAccount.name` | string | — (agent name) | Required when `create=false`. |
| `serviceAccount.annotations` | map[string]string | — | Keyless cloud auth (IRSA/Workload Identity). |
| `serviceAccount.automountToken` | *bool | `true` | |
| `probes.mode` | `auto`\|`exec`\|`http` | `auto` | `auto` = http when apiServer enabled, else exec. `http` requires `apiServer.enabled`. |
| `probes.liveness` / `probes.readiness` | ProbeSettings | — | `initialDelaySeconds`/`periodSeconds`/`timeoutSeconds`/`failureThreshold`/`successThreshold`. |
| `kubeconfig.enabled` | bool | `false` | Write `~/.kube/config` from the pod SA token. Requires `serviceAccount.automountToken != false`. |

### Validation (CEL)

- `storage.existingClaim` is mutually exclusive with `storage.size`/`storageClassName` (set exactly one).
- `apiServer.enabled` requires `keySecretRef` and a non-localhost `host`.
- `probes.mode=http` requires `apiServer.enabled`.
- `ingress.enabled` requires `dashboard.enabled` and `ingress.host`.
- `apiServer.ingress.enabled` requires `apiServer.enabled` and a host (`apiServer.ingress.host` or `spec.ingress.host`).
- `channels[].ingress.enabled` requires a host (`channels[].ingress.host` or `spec.ingress.host`).
- each `skills.custom[]` must set exactly one of `sourceRef` or `inline`.
- each `mcp.servers[]` must set exactly one of `command` (stdio) or `url` (http/sse); `transport` must match; `tools` sets at most one of `include`/`exclude`.
- `serviceAccount.name` is required when `serviceAccount.create=false`.
- `kubeconfig.enabled` requires `serviceAccount.automountToken != false`.

---

# Examples

### Minimal — a built-in provider (Claude)

```yaml
apiVersion: hermes.nousresearch.io/v1alpha1
kind: HermesAgent
metadata:
  name: claude-bot
  namespace: agents
spec:
  image: harbor.bne1.ouchi.com.au/applications/hermes-agent:v2026.5.16
  imagePullSecrets: [{ name: harbor-pull }]
  storage: { size: 20Gi, storageClassName: fast-ssd }
  model:
    default: claude-opus-4-7
    provider: claude              # alias -> anthropic; key -> ANTHROPIC_API_KEY
    providers:
      - name: claude
        keySecretRef: { name: model-keys, key: anthropic }
  dashboard: { enabled: true }
  ingress:
    enabled: true
    host: claude-bot.example.com
    className: nginx
    tls: [{ hosts: [claude-bot.example.com], secretName: claude-bot-tls }]
```

### Multiple providers, switchable

```yaml
spec:
  model:
    default: claude-opus-4-7
    provider: claude              # active; /model can switch to the others
    providers:
      - { name: claude,  keySecretRef: { name: model-keys, key: anthropic } }
      - { name: grok,    keySecretRef: { name: model-keys, key: xai } }       # -> XAI_API_KEY
      - { name: openai,  keySecretRef: { name: model-keys, key: openrouter } } # -> OPENROUTER_API_KEY
```

### Self-hosted OpenAI-compatible endpoint with per-model context

```yaml
spec:
  model:
    default: gemma-4-e4b-it
    provider: llm-bne1            # selects the custom provider; base_url/api_mode derived
    providers:
      - name: llm-bne1
        baseURL: https://llm.bne1.ouchi.com.au/v1
        apiMode: chat_completions
        # keySecretRef omitted -> keyless endpoint (or seed auth.json)
        models:
          - { name: gemma-4-e4b-it,  contextLength: 131072 }
          - { name: qwen35-9b-opus,  contextLength: 65536 }
```

### Docker terminal backend + in-cluster kubeconfig + brew tools

```yaml
spec:
  runAsRoot: false
  serviceAccount: { create: false, name: agent-sa, automountToken: true }
  storage: { size: 50Gi, storageClassName: proxmox-local-ext4, reclaimPolicy: Retain }
  runtime:
    terminalBackend: docker        # operator injects a DinD sidecar on the shared PVC
  kubeconfig: { enabled: true }     # ~/.kube/config from the pod SA (kubectl works in-pod and in dind)
  packages:
    brew: [kubectl]                 # persisted on the PVC, also bind-mounted into dind
```

### Messaging channels (telegram webhook + discord polling)

```yaml
spec:
  ingress: { enabled: true, host: bot.example.com }   # shared host
  dashboard: { enabled: true }
  channels:
    - type: telegram
      secretRef: { name: bot-channels }   # must include TELEGRAM_WEBHOOK_SECRET for webhook mode
      ingress: { enabled: true }          # -> https://bot.example.com/webhooks/telegram (auto port)
    - type: discord
      secretRef: { name: bot-channels }   # outbound gateway websocket; webhookPort 0 (polling)
```

### OpenAI-compatible API server + extraConfig escape hatch

```yaml
spec:
  apiServer:
    enabled: true
    host: 0.0.0.0
    corsOrigins: ["*"]
    keySecretRef: { name: agent-secret, key: API_SERVER_KEY }
    ingress: { enabled: true }            # inherits spec.ingress host/tls
  ingress: { enabled: true, host: api.example.com }
  # Anything not modeled as a typed field can be merged into config.yaml:
  extraConfig:
    fallback_model: { provider: openrouter, model: anthropic/claude-sonnet-4 }
    auxiliary:
      compression: { provider: openrouter, model: google/gemini-3-flash }
```

### MCP servers (stdio + http with a secret token)

```yaml
spec:
  mcp:
    servers:
      # stdio: launched as a subprocess; token interpolated into its env.
      - name: github
        command: npx
        args: ["-y", "@modelcontextprotocol/server-github"]
        env:
          GITHUB_PERSONAL_ACCESS_TOKEN: ${GH_TOKEN}
        secretEnv:
          - name: GH_TOKEN
            secretRef: { name: mcp-secrets, key: github-token }
        tools:
          include: [create_issue, get_issue]   # narrow the exposed tools
      # http/sse: remote endpoint with a bearer token from a Secret.
      - name: remote
        transport: sse
        url: https://mcp.example.com/sse
        headers:
          Authorization: Bearer ${REMOTE_TOKEN}
        secretEnv:
          - name: REMOTE_TOKEN
            secretRef: { name: mcp-secrets, key: remote-token }
        extraConfig:                            # long-tail knobs (oauth, sampling)
          sampling: { enabled: true, max_rpm: 10 }
```

A complete, production CR (custom endpoint + docker + kubeconfig + channels +
searxng + honcho + dashboard ingress) lives at
`k8s-cluster/ouchi/bne1-cluster1/hermes/lana-hermesagent.yaml`.

---

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
