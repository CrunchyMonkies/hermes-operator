# Hermes Operator — Specification

> A Kubernetes operator, written in Go, for deploying and managing
> [Nous Research Hermes Agent](https://github.com/nousresearch/hermes-agent)
> instances declaratively via Custom Resources.

Status: **Draft** · Target: greenfield (`hermes-operator`) · Owner: matthew

**Pinned upstream:** Hermes Agent `v2026.5.16` (release v0.14.0, commit `a91a57fa5`),
vendored at `third_party/hermes-agent` as a git submodule. All concrete claims
below (env vars, ports, file paths, config keys) are verified against that tag's
source — primarily `Dockerfile`, `docker/entrypoint.sh`, `cli-config.yaml.example`,
`.env.example`, `gateway/platforms/api_server.py`, `gateway/status.py`,
`hermes_constants.py`, `tools/skills_sync.py`, and `hermes_cli/skills_config.py`.
When bumping the pin, re-verify §3, §6, §7, §8, §9.

---

## 1. Background — verified runtime facts

Hermes Agent's deployable unit is the **gateway** (`hermes gateway run`): a
long-lived process that connects messaging platforms (Telegram, Discord, Slack,
WhatsApp, Signal, Email, Teams, Google Chat, …) and can optionally serve an
OpenAI-compatible HTTP API and a separate web dashboard.

| Aspect | Verified value | Source |
| --- | --- | --- |
| Image | `nousresearch/hermes-agent` / `ghcr.io/nousresearch/hermes-agent` | docker-compose.yml |
| Base | `debian:13.4`, Python 3.13 via `uv`; build tools (`gcc`, `build-essential`) present | Dockerfile |
| ENTRYPOINT | `tini -g -- /opt/hermes/docker/entrypoint.sh` | Dockerfile |
| Gateway command | `["gateway", "run"]` | docker-compose.yml |
| `HERMES_HOME` (data) | `/opt/data` (VOLUME) | Dockerfile `ENV HERMES_HOME=/opt/data` |
| **hermes `$HOME`** | **`/opt/data`** (`useradd -m -d /opt/data`) ⇒ `~/.local` = `/opt/data/.local` | Dockerfile |
| **PATH** | already includes **`/opt/data/.local/bin`** | Dockerfile `ENV PATH=` |
| Runtime user | non-root `hermes`, **UID/GID default 10000** | Dockerfile |
| Privilege model | **starts as root**, entrypoint `usermod`/`groupmod` then `gosu hermes` | entrypoint.sh |
| API server port | **8642** (`DEFAULT_PORT`), bind `127.0.0.1` (`DEFAULT_HOST`) | api_server.py:57-58 |
| API server state | **OFF by default**; `API_SERVER_ENABLED` / `API_SERVER_KEY` enables | config.py:1472 |
| Health endpoint | `/health` + `/health/detailed` — **only on the API server** | api_server.py:3363 |
| Dashboard | separate `dashboard` cmd; or backgrounded when `HERMES_DASHBOARD=1` | entrypoint.sh |
| Dashboard port | `HERMES_DASHBOARD_PORT` default **9119**, host `0.0.0.0` (+auto `--insecure`) | entrypoint.sh |
| Skills dir | `~/.hermes/skills` = **`/opt/data/skills`**; seeded by `skills_sync.py` at boot | skills_sync.py |
| Playwright browsers | `/opt/hermes/.playwright` — **outside** the volume | Dockerfile |
| Homebrew | **not present upstream** — added by our derived image (§5) | — |

### 1.1 Two single-writer guarantees (the dominant design fact)

1. **Storage:** sessions/memory are SQLite + flat files under `/opt/data`. Two
   gateways writing the same directory corrupts state.
2. **App-level lock:** the gateway acquires `gateway.lock` in `HERMES_HOME` via
   `fcntl.flock(LOCK_EX | LOCK_NB)` (`gateway/status.py`); a second process on
   the same volume **fails to acquire and refuses to start**.

**Consequence:** every `HermesAgent` is a singleton — `replicas ∈ {0,1}`, one
ReadWriteOnce PVC, `Recreate` update strategy (old pod fully terminates and
releases the volume + lock before the new pod starts). A reloader **sidecar**
(§8) shares the pod but must never start a second gateway.

### 1.2 Health-probe reality

There is **no always-on HTTP health port** for a bare `gateway run`. `/health`
exists only when the API server platform is active, and the API server **refuses
to bind to a non-localhost address without `API_SERVER_KEY`** (api_server.py:3399).
Two probe modes:

- **`exec` (default):** `hermes gateway status` (real subcommand). Works
  regardless of enabled platforms.
- **`httpGet` (opt-in):** auto-selected when `spec.apiServer.enabled: true`,
  probing `GET /health` on 8642 (requires `apiServer.keySecretRef`).

### 1.3 First-boot bootstrap (entrypoint.sh)

As root, the entrypoint may remap UID/GID, `chown -R` the volume, drop to
`hermes`, create the dir tree (`cron sessions logs hooks memories skills skins
plans workspace home`), then seed (only if absent): `.env` ← `.env.example`,
`config.yaml` ← `cli-config.yaml.example`, `SOUL.md` ← `docker/SOUL.md`,
`auth.json` ← `$HERMES_AUTH_JSON_BOOTSTRAP` (once). It re-`chown`/`chmod 640`s
`config.yaml` every boot, then runs `tools/skills_sync.py` to seed bundled
skills. **Our derived image extends this entrypoint** for brew-prefix init and
package application (§5, §8).

### 1.4 Skills model (verified)

- Bundled skills (25 top-level categories: `software-development`, `devops`,
  `research`, `github`, `email`, …) ship in the image's `skills/` dir and are
  copied to `/opt/data/skills/` by `skills_sync.py`, manifest-tracked so user
  edits are preserved.
- A skill is a directory containing `SKILL.md` with YAML frontmatter
  (`name`, `description`, `version`, `platforms`, `metadata.hermes.tags`).
- **Enablement is disable-based**, persisted in `config.yaml` under `skills:`
  (`hermes_cli/skills_config.py`):
  - `skills.disabled: [names]` — globally disabled
  - `skills.platform_disabled.<platform>: [names]` — per-platform disabled
  - `skills.external_dirs: [paths]` — read-only external skill dirs
  - `skills.creation_nudge_interval: N`
- ⇒ **All bundled (default) skills are active unless listed in `disabled`.**
  "Activating default skills" = ensuring they're synced and not disabled.
  Adding a custom skill = writing its dir under `/opt/data/skills/<name>/` and
  keeping it out of `disabled`.

---

## 2. Goals & Non-Goals

### Goals
1. Declarative lifecycle for Hermes gateway instances via a `HermesAgent` CRD.
2. Render `config.yaml` from typed CRD fields (incl. `${VAR}` interpolation).
3. Reference credentials via `Secret`s — operator never reads secret material.
4. **Single shared PVC** for all mounts, with **settable size** (§4).
5. Strict single-writer guarantees (§1.1).
6. **Declarative skill activation** — control default-skill enablement and ship
   operator-provided custom skills, auto-activated (§7).
7. **Declarative package installation** via **apt** and **Homebrew (brew)**,
   installed without sudo, with brew persisted on the shared PVC; `~/.local/bin`
   on PATH and `~/.local` on the PVC (§5).
8. **Executor/reloader** that applies config/skill/package changes from the
   Kubernetes resources to the running agent (§8).
9. Expose API server / dashboard / messaging webhook ports via `Service` **and
   optional `Ingress` with custom annotations, host, path, and TLS**; allow a
   full **`podTemplate` overlay** to configure any pod option (scheduling,
   sidecars, volumes, securityContext, labels/annotations, …) over the
   operator-rendered base.
10. Health-gated rollouts; restart only when required.
11. **Per-component Dockerfiles**, built and pushed to
    `harbor.bne1.ouchi.com.au/applications/` (§9).

### Non-Goals (v1)
- Horizontal scaling of a single agent (impossible — §1.1).
- Provisioning the LLM providers or messaging platforms themselves.
- Provisioning ingress controllers or cert-manager themselves (we emit
  `Ingress` objects + annotations; the cluster supplies the controller/issuer).
- Persisting **apt** packages across pod recreation (apt is system-level and
  re-applied each boot — bake a derived image for heavy/stable deps; §5.3).
- Backup/restore of agent state (future — §16 roadmap).

---

## 3. Custom Resource Definitions

Group `hermes.nousresearch.io`, version `v1alpha1`, namespace-scoped.

### 3.1 `HermesAgent` (primary)

One CR = one gateway = one Deployment(`Recreate`, replicas≤1) + **one shared
PVC** + one ConfigMap + one Service (+ reloader sidecar).

```yaml
apiVersion: hermes.nousresearch.io/v1alpha1
kind: HermesAgent
metadata:
  name: research-bot
spec:
  image: harbor.bne1.ouchi.com.au/applications/hermes-agent:v2026.5.16
  imagePullPolicy: IfNotPresent
  imagePullSecrets: [{ name: harbor-pull }]

  presetRef: { name: standard-opus }            # optional (3.2)
  soul: |                                        # -> SOUL.md
    You are a research assistant...

  model:
    default: anthropic/claude-opus-4.6
    provider: auto
    baseURL: ""
    contextLength: 0                             # hot-reloadable (4.3)
  agent:
    maxTurns: 60
    reasoningEffort: medium
    disabledToolsets: []
  compression:                                   # hot-reloadable (4.3)
    enabled: true
    threshold: 0.50
    targetRatio: 0.20
    protectLastN: 20
  memory:
    memoryEnabled: true
    userProfileEnabled: true

  apiServer:
    enabled: false
    host: 0.0.0.0
    port: 8642
    keySecretRef: { name: research-bot-secrets, key: api-server-key }  # required if enabled
    corsOrigins: []
    service:                                     # see 3.5
      type: ClusterIP                            # ClusterIP | NodePort | LoadBalancer
      annotations: {}                            # custom Service annotations (e.g. MetalLB, cloud LB)
    ingress:                                     # see 3.5
      enabled: true
      className: nginx                           # spec.ingressClassName
      host: api.research-bot.example.com
      path: /
      pathType: Prefix
      annotations:                               # arbitrary custom annotations, merged verbatim
        nginx.ingress.kubernetes.io/proxy-body-size: "25m"
        cert-manager.io/cluster-issuer: letsencrypt-prod
      tls:
        - secretName: research-bot-api-tls
          hosts: [api.research-bot.example.com]
  dashboard:
    enabled: false
    host: 0.0.0.0
    port: 9119
    service:
      type: ClusterIP
      annotations: {}
    ingress:
      enabled: false
      className: nginx
      host: dashboard.research-bot.example.com
      path: /
      pathType: Prefix
      annotations: {}                            # e.g. add auth: nginx.ingress.kubernetes.io/auth-url
      tls: []

  channels:
    - type: telegram                             # telegram|discord|slack|whatsapp|signal|email|teams|google_chat
      enabled: true
      secretRef: { name: research-bot-secrets }
      webhookPort: 0                             # 0 = polling; >0 exposes port on Service
      ingress:                                   # optional; only meaningful when webhookPort > 0
        enabled: false
        className: nginx
        host: tg.research-bot.example.com
        path: /telegram
        pathType: Prefix
        annotations: {}
        tls: []

  # ---- Skills (7) ----
  skills:
    # Default (bundled) skills are active unless disabled. Use these to trim.
    disabled: []                                 # -> config.yaml skills.disabled
    platformDisabled: {}                         # -> skills.platform_disabled.<platform>
    externalDirs: []                             # -> skills.external_dirs
    creationNudgeInterval: 15
    # Operator-shipped custom skills, written into the PVC and auto-activated.
    # `source` may be inline content or a ConfigMap/Secret ref holding SKILL.md (+files).
    custom:
      - name: package-management                 # see 7.2 (added & activated automatically)
        enabled: true
        sourceRef: { configMapName: research-bot-skill-pkg }   # or `inline:`
    # Convenience toggle: ensure the operator's package-management skill is
    # present and enabled even if not listed above. Defaults true (per brief).
    enablePackageManagementSkill: true

  # ---- Packages (5) ----
  packages:
    apt:                                          # installed at boot as root (pre-gosu); NOT persisted (5.3)
      - ripgrep
      - jq
    brew:                                         # installed to shared PVC; persisted (5.1)
      - gh
      - fd
    # Homebrew prefix lives on the shared PVC; see 5.1. Override only if needed.
    homebrewPrefix: /home/linuxbrew/.linuxbrew

  # ---- Agent runtime: tool/code execution backend (see 11) ----
  runtime:
    terminalBackend: local            # local|docker|ssh|modal|daytona|vercel_sandbox|singularity
    terminalTimeout: 180              # -> terminal.timeout
    codeExecution:
      timeout: 300                    # -> code_execution.timeout
      maxToolCalls: 50                # -> code_execution.max_tool_calls
    delegation:
      maxConcurrentChildren: 3        # -> delegation (batch parallelism)
      maxIterations: 50
    # When terminalBackend: docker, the operator injects a Docker-in-Docker
    # sidecar and wires the agent to it (see 11.2). The SAME shared-PVC subPath
    # mounts are mapped into the dind container at IDENTICAL paths so bind mounts
    # the agent requests resolve correctly inside the daemon.
    docker:
      image: docker:27-dind           # dind sidecar image
      rootless: false                 # true => docker:27-dind-rootless (no privileged)
      socketTransport: unix           # unix (shared emptyDir socket) | tcp
      mountCwdToWorkspace: false       # -> terminal.docker_mount_cwd_to_workspace
      resources:                       # dind sidecar resources
        requests: { cpu: "250m", memory: 1Gi }
        limits:   { cpu: "2",    memory: 4Gi }
      storage:                         # dind /var/lib/docker (image/layer store)
        size: 20Gi                     # separate subPath on the SAME shared PVC (4.1)

  # ---- Scheduled tasks (cron) — see 12 ----
  cron:
    # Declaratively seed jobs into /opt/data/cron/jobs.json (reconciled by the
    # reloader). The agent may also create jobs at runtime; `pruneUnmanaged`
    # controls whether operator-unknown jobs are removed.
    pruneUnmanaged: false
    jobs:
      - name: daily-digest
        schedule: "0 8 * * *"         # cron expr; runs in-gateway scheduler
        prompt: "Summarize overnight activity and post to the home channel."

  # ---- Credentials / env ----
  envFrom: [{ secretRef: { name: research-bot-secrets } }]
  env:
    - name: OPENROUTER_API_KEY
      valueFrom: { secretKeyRef: { name: research-bot-secrets, key: openrouter } }
  authJSONBootstrapSecretRef: { name: research-bot-secrets, key: auth-json }

  # ---- Service account (see 3.7) ----
  # SA the agent pod + reloader sidecar run as. Authoritative over any
  # podTemplate.spec.serviceAccountName.
  serviceAccount:
    create: true                  # operator creates a per-agent SA + binds the reloader Role
    name: ""                      # name to create/use; "" => "<agent-name>" ; required if create=false
    annotations:                  # e.g. cloud workload identity for keyless provider auth
      eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/hermes-bedrock
    # GKE: iam.gke.io/gcp-service-account; Azure: azure.workload.identity/client-id
    automountToken: true          # set false to drop the projected k8s token if unused

  extraConfig: { web: { backend: tavily } }       # deep-merged into config.yaml
  extraConfigPrecedence: merge                     # merge | override

  # ---- Storage: ONE shared PVC, settable size (4) ----
  storage:
    size: 20Gi                                     # the only size knob (brief: "set the size")
    storageClassName: ""
    existingClaim: ""                              # XOR size/storageClassName
    reclaimPolicy: Retain                          # Retain | Delete
    # Sub-paths of the single claim mounted at multiple locations (4.1):
    #   data: /opt/data   dotlocal: /opt/data/.local   linuxbrew: /home/linuxbrew/.linuxbrew

  replicas: 1                                      # validated 0 or 1
  updateStrategy: Recreate
  resources:
    requests: { cpu: "500m", memory: 2Gi }
    limits:   { cpu: "2",    memory: 4Gi }
  shmSize: 1Gi                                     # /dev/shm for browser tools

  # ---- Pod template overlay: configure ANY pod option (see 3.6) ----
  # A full corev1.PodTemplateSpec, strategic-merged OVER the operator-rendered
  # base pod. Use it for scheduling (nodeSelector/affinity/tolerations/
  # topologySpread/priorityClassName/schedulerName/runtimeClassName), extra
  # sidecars/initContainers, extra volumes, serviceAccountName, hostAliases,
  # dnsConfig, pod/container securityContext additions, labels/annotations, etc.
  # Operator invariants (shared-PVC mounts on the hermes container, single-writer
  # guarantees, config-hash annotation, root-start-for-gosu) are re-asserted
  # after the overlay and always win — see 3.6.
  podTemplate:
    metadata:
      labels:      { team: research }
      annotations: { prometheus.io/scrape: "true" }
    spec:
      nodeSelector: { kubernetes.io/arch: amd64 }
      tolerations:
        - { key: dedicated, operator: Equal, value: hermes, effect: NoSchedule }
      affinity: {}
      topologySpreadConstraints: []
      priorityClassName: ""
      schedulerName: ""
      runtimeClassName: ""
      # serviceAccountName: set via spec.serviceAccount (3.7), not here
      hostAliases: []
      dnsConfig: {}
      # Add your own sidecars/initContainers/volumes here; merged with the
      # operator's hermes container + reloader sidecar by container name.
      containers: []

  hermesUID: 10000
  hermesGID: 10000
  runAsRoot: true                                  # required for usermod+gosu+apt (4.2)
  fsGroup: 10000

  probes:
    mode: auto                                     # auto | exec | http
    liveness:  { initialDelaySeconds: 40, periodSeconds: 30, failureThreshold: 4 }
    readiness: { initialDelaySeconds: 15, periodSeconds: 15, failureThreshold: 3 }

status:
  phase: Running                                   # Pending|Provisioning|Running|Degraded|Failed|Paused
  observedGeneration: 4
  configHash: "sha256:…"
  readyReplicas: 1
  serviceName: research-bot
  endpoints: { api: "research-bot:8642", dashboard: "research-bot:9119" }
  skills:    { synced: 142, disabled: 0, customActive: ["package-management"] }
  packages:  { aptApplied: ["ripgrep","jq"], brewInstalled: ["gh","fd"] }
  conditions:
    - { type: Ready,          status: "True", reason: GatewayHealthy }
    - { type: ConfigInSync,   status: "True" }
    - { type: VolumeBound,    status: "True" }
    - { type: SkillsApplied,  status: "True" }
    - { type: PackagesApplied,status: "True" }
```

#### Validation (CEL + optional webhook)
- `replicas ∈ {0,1}`.
- `storage.existingClaim` XOR (`storage.size` | `storageClassName`).
- `apiServer.enabled` ⇒ `keySecretRef` set AND `host != 127.0.0.1`.
- `probes.mode == http` ⇒ `apiServer.enabled`.
- `packages.brew` non-empty ⇒ image must contain Homebrew dist (our derived
  image; warn if `image` looks like upstream `nousresearch/...`).
- `skills.custom[].sourceRef` XOR `skills.custom[].inline`.
- `runAsRoot == false` ⇒ warn: UID/GID remap, volume `chown`, and **apt install**
  are skipped (apt needs root).
- `runtime.terminalBackend == docker` ⇒ a dind sidecar is injected (§11.2); warn
  that standard (non-`rootless`) DinD needs `privileged` and is incompatible with
  `restricted` Pod Security Standards. `rootless: true` drops the privileged
  requirement.
- `apiServer.ingress.enabled` ⇒ `apiServer.enabled` (no backend otherwise).
- `dashboard.ingress.enabled` ⇒ `dashboard.enabled`.
- `channels[].ingress.enabled` ⇒ `channels[].webhookPort > 0`.
- any `ingress.enabled` ⇒ `ingress.host` non-empty; `tls[].secretName` references
  a TLS Secret (existence not required at admission — cert-manager may create it).
- `podTemplate` (webhook): reject overlays that violate invariants (§3.6) — a
  volume named like the shared claim remapped off `/opt/data`, `replicas` set via
  the template, a `serviceAccountName` conflicting with `spec.serviceAccount`, or
  `runAsNonRoot: true`/`runAsUser != 0` on the `hermes` container while
  `runAsRoot: true`. Extra sidecars/volumes/scheduling are always allowed.
- `serviceAccount.create == false` ⇒ `serviceAccount.name` required.

### 3.2 `HermesConfigPreset` (supporting)
Reusable bundle of `model`/`agent`/`compression`/`memory`/`skills`/`packages`/
`extraConfig` defaults; `presetRef` deep-merges preset → CR (CR wins).

### 3.3 `HermesChannel` (supporting, v1beta1)
Standalone messaging-platform binding referenced via `channelRefs`. v1 uses
inline `spec.channels`. Verified per-platform env surface:

| type | token env | allow-list env | port env |
| --- | --- | --- | --- |
| telegram | `TELEGRAM_BOT_TOKEN` | `TELEGRAM_ALLOWED_USERS` | `TELEGRAM_WEBHOOK_PORT/URL/SECRET` |
| discord | `DISCORD_BOT_TOKEN` | (config channels) | — |
| slack | `SLACK_BOT_TOKEN`,`SLACK_APP_TOKEN` | `SLACK_ALLOWED_USERS` | — |
| whatsapp | `WHATSAPP_ENABLED` | `WHATSAPP_ALLOWED_USERS` | — |
| email | `EMAIL_ADDRESS/PASSWORD/IMAP_HOST/SMTP_HOST` | `EMAIL_ALLOWED_USERS` | `EMAIL_POLL_INTERVAL` |
| teams | `TEAMS_CLIENT_ID/SECRET/TENANT_ID` | `TEAMS_ALLOWED_USERS` | `TEAMS_PORT` (3978) |
| google_chat | `GOOGLE_CHAT_PROJECT_ID/SUBSCRIPTION_NAME/SERVICE_ACCOUNT_JSON` | `GOOGLE_CHAT_ALLOWED_USERS` | — |

### 3.4 Mapped config.yaml surface
Typed fields render to the matching `config.yaml` sections. Verified top-level
sections: `model terminal browser tool_loop_guardrails compression
prompt_caching memory session_reset streaming skills agent platform_toolsets
stt code_execution delegation display`. Anything unmodeled is reachable via
`extraConfig` (deep-merged).

### 3.5 Networking — Service & Ingress

Each HTTP-exposed surface (API server 8642, dashboard 9119, and any channel
webhook port) gets:

- A **`Service`** whose `type` and `annotations` are taken from the surface's
  `service:` block (so `LoadBalancer`/`NodePort` and cloud/MetalLB annotations
  are supported). Ports are derived from the enabled surfaces; one Service per
  agent carries all enabled ports (named `api`, `dashboard`, `wh-<channel>`).
- An optional **`Ingress`** from the surface's `ingress:` block:
  `enabled`, `className` (→ `spec.ingressClassName`), `host`, `path`+`pathType`,
  **`annotations` merged verbatim** onto the Ingress object (controller-agnostic —
  nginx, Traefik, cert-manager, external-dns, auth, rate-limit, body-size, etc.),
  and `tls[]` (`secretName` + `hosts`). The operator owns these Ingress objects
  (owner refs ⇒ GC) and re-renders them on change. Ingress edits **never restart
  the gateway** (they don't touch `configHash`'s pod-template inputs).

Operator-managed annotations are namespaced under `hermes.nousresearch.io/*` and
never collide with user annotations; on conflict the **user annotation wins** for
keys the operator does not own.

### 3.6 Pod template overlay (`spec.podTemplate`)

Rather than re-modeling every pod knob, `spec.podTemplate` is a full
`corev1.PodTemplateSpec` (optional) **strategic-merged over the operator-rendered
base pod template**. This makes *any* pod option configurable: scheduling
(`nodeSelector`, `affinity`, `tolerations`, `topologySpreadConstraints`,
`priorityClassName`, `schedulerName`, `runtimeClassName`), extra
sidecars/initContainers, extra volumes/mounts, `serviceAccountName`,
`hostAliases`, `dnsConfig`/`dnsPolicy`, `terminationGracePeriodSeconds`,
pod/container `securityContext` additions, and pod `labels`/`annotations`.

**Merge order (last writer wins, except invariants):**
1. Operator renders the **base** pod template (hermes container + reloader
   sidecar, shared-PVC volume + subPath mounts, `/dev/shm` emptyDir, env/envFrom,
   probes, `config-hash` annotation, root-start/gosu, `fsGroup`).
2. `spec.podTemplate` is **strategic-merge-patched** over the base. Containers,
   volumes, initContainers, tolerations, etc. merge by their patch-merge keys
   (e.g. container `name`, volume `name`) so users **extend** rather than
   clobber — e.g. adding a log-tail sidecar, a custom volume, or an env var.
3. The operator **re-asserts invariants** after the overlay and always wins on:
   - the shared-PVC volume + its `/opt/data` and `/home/linuxbrew/.linuxbrew`
     subPath mounts on the `hermes` container (protects single-writer + brew),
   - `replicas`/`Recreate` (set on the Deployment, not the template),
   - the `config-hash` pod annotation,
   - the requirement that the `hermes` container starts as root unless
     `runAsRoot: false` (the entrypoint needs it for `usermod`+`gosu`+apt).

Validation rejects/warns on overlays that would violate an invariant (e.g.
mounting a different volume at `/opt/data`, setting `replicas` via the template,
or forcing `runAsNonRoot: true` while `runAsRoot: true`). Typed top-level fields
that the operator reasons about semantically — `resources`, `shmSize`, `probes`,
`hermesUID/GID`, `runAsRoot`, `fsGroup` — remain first-class; when both are set,
the typed field is authoritative for its specific key and the rest of the
overlay still applies. This affects only the single pod and does not change the
single-writer constraint (§1.1).

### 3.7 Service account (`spec.serviceAccount`)

The agent pod **and its reloader sidecar** run under one ServiceAccount. Two modes:

- **`create: true` (default):** the operator creates a per-agent SA named
  `name` (or `<agent-name>`), owner-ref'd to the `HermesAgent`, and binds the
  scoped **reloader Role** (§10) to it via a `RoleBinding`. This is required
  whenever the reloader reads its CR/ConfigMaps through the API.
- **`create: false`:** the operator uses an **existing** SA (`name` required) and
  does **not** manage its RBAC — the caller is responsible for binding the
  reloader Role. Use this when the SA is owned by another controller.

`annotations` are applied to the SA (created or, when `create: true`, patched) —
the mechanism for **keyless cloud provider auth**: AWS IRSA
(`eks.amazonaws.com/role-arn`), GKE Workload Identity
(`iam.gke.io/gcp-service-account`), or Azure Workload Identity
(`azure.workload.identity/client-id`), letting the agent reach Bedrock/Vertex/
Azure OpenAI without static keys in a Secret. `automountToken: false` drops the
projected token when no in-cluster API access is needed (note: the reloader's
API watch needs it; validation warns if both `reloader API watch` and
`automountToken: false` are set).

`spec.serviceAccount` is **authoritative** over any
`podTemplate.spec.serviceAccountName` (validation rejects a conflicting value in
the overlay). The operator's *own* SA is separate and managed by the
`hermes-operator` Helm chart / kustomize (§9.3a, §10).

---

## 4. Storage — one shared PVC

Per the brief, **all mounts use the same PVC** and **size is settable**
(`spec.storage.size`). Implementation: a single RWO `PersistentVolumeClaim` per
agent, mounted at multiple paths in the one pod via `subPath`.

### 4.1 Mount map (single claim)

| Mount path | `subPath` | Purpose |
| --- | --- | --- |
| `/opt/data` | `data` | `HERMES_HOME` + hermes `$HOME` ⇒ `skills/`, `sessions/`, `memories/`, `gateway.lock`, config. Also mounted at the **same path in the dind sidecar** when docker backend (§11.2). |
| `/opt/data/.local` | `dotlocal` | hermes `~/.local` — user-installed binaries (`~/.local/bin` on PATH, pip `--user`, npm `-g`); persisted independently of agent data. Same-path mount in dind when docker backend. |
| `/home/linuxbrew/.linuxbrew` | `linuxbrew` | Homebrew prefix/Cellar (default prefix ⇒ bottle support), persisted, no sudo (§5.1). Same-path mount in dind when docker backend. |
| `/var/lib/docker` (dind only) | `dind` | DinD daemon image/layer store; present only when `terminalBackend: docker`, sized by `runtime.docker.storage.size` (§11.2). |

Since the hermes user's `$HOME` is `/opt/data`, `~/.local` is `/opt/data/.local`
and `/opt/data/.local/bin` is already on `PATH` (Dockerfile). We mount it as its
**own `subPath` (`dotlocal`) of the same claim**, nested under the `/opt/data`
mount, so user-installed tooling lives in a dedicated subtree decoupled from
session/memory data — directly satisfying the brief's "`~/.local` should be
mapped to a PVC." Kubernetes mounts the more-specific path
(`/opt/data/.local`) after the parent, so the dedicated subPath wins for that
path.

Mounting the same RWO claim at multiple (and nested) `subPath`s within a
**single pod** is allowed and does not violate single-writer (a cross-pod
constraint).

### 4.2 Security context — image self-drops root + apt needs root
The container **must start as root** so `entrypoint.sh` can remap UID/GID,
`chown -R` the shared volume, **install apt packages (root)**, then
`exec gosu hermes`. Default: `runAsNonRoot: false`, no `runAsUser`; `tini`(PID 1)
+ entrypoint drop to UID 10000; `fsGroup: 10000`. If policy forbids root
(`runAsRoot: false`): set `runAsUser: 10000`+`fsGroup: 10000`, **apt is skipped**
(brew still works), and the PVC must be owned by 10000.

### 4.3 Restart vs hot-reload
Upstream hot-reloads only `model.context_length` and `compression.*`. v1 uses
`subPath` ConfigMap mounts (no live propagation), so even those trigger a pod
restart; the reloader (§8) can apply them in-place as a v1beta1 optimization.
`configHash` includes referenced Secrets' `resourceVersion` (rotation rolls the
pod without the operator decrypting values).

---

## 5. Package management (apt + Homebrew)

The brief requires runtime application installation via **apt or brew**, brew
baked into the image, installed to a **writable PVC without sudo**, with
`~/.local/bin` on PATH.

### 5.1 Homebrew on the shared PVC (no sudo, persisted)
- Our derived `hermes-agent` image (§9) bakes a Homebrew distribution at an
  **unmounted** path (e.g. `/opt/homebrew-dist`) — a PVC mount would otherwise
  mask image content.
- Env baked in: `HOMEBREW_PREFIX=/home/linuxbrew/.linuxbrew`,
  `HOMEBREW_CELLAR=$HOMEBREW_PREFIX/Cellar`,
  `HOMEBREW_REPOSITORY=$HOMEBREW_PREFIX/Homebrew`, `HOMEBREW_NO_ANALYTICS=1`,
  and `PATH` prepended with `$HOMEBREW_PREFIX/bin:$HOMEBREW_PREFIX/sbin`
  (alongside the existing `/opt/data/.local/bin`).
- **Default prefix `/home/linuxbrew/.linuxbrew`** is mounted from the shared PVC
  (`subPath: linuxbrew`, §4.1) so installs persist and **bottles (prebuilt
  binaries) work** — avoiding slow source builds.
- **First-boot init** (entrypoint extension, runs as `hermes`, no sudo): if
  `$HOMEBREW_PREFIX/bin/brew` is absent, populate the prefix from
  `/opt/homebrew-dist` (rsync/clone) — idempotent across restarts. The prefix is
  owned by gid 10000 via `fsGroup`/chown, so brew runs as the non-root `hermes`
  user exactly as it expects.
- Brew packages from `spec.packages.brew` are ensured idempotently at boot by
  the reloader (§8): `brew install <pkg>` for any missing formula.

### 5.2 `~/.local` (dedicated PVC subPath)
`~/.local` = `/opt/data/.local` (hermes `$HOME` = `/opt/data`) and
`/opt/data/.local/bin` is already on `PATH` (Dockerfile). The operator mounts it
as its **own `subPath` (`dotlocal`) of the shared claim** at `/opt/data/.local`
(§4.1) so pip `--user`, `npm -g` (prefix `~/.local`), `uv tool`, and brew shims
persist in a dedicated subtree, independent of session/memory data — while still
being part of the single shared PVC.

### 5.3 apt (root, boot-time, ephemeral)
`spec.packages.apt` packages are installed by the entrypoint's **root stage**
(before `gosu`) via `apt-get install -y --no-install-recommends`. These land in
system dirs **not on the PVC**, so they are **re-applied on every pod start**
(adds startup latency; needs egress to apt mirrors). For heavy/stable system
deps, bake a derived image instead. Requires `runAsRoot: true` (§4.2).

---

## 6. Skills activation

Per the brief: cover **activation of the default skills** and **add+auto-activate
a skill** (for package management).

### 6.1 Default skills
`skills_sync.py` seeds all bundled skills into `/opt/data/skills` at boot; they
are active unless disabled. The CRD's `spec.skills.disabled` /
`platformDisabled` render into `config.yaml` `skills.disabled` /
`skills.platform_disabled`. Empty lists ⇒ **all defaults active**. The reloader
applies changes via the `skills_config` module / `hermes skills config` without
a full rebuild where possible.

### 6.2 Operator-provided custom skills (auto-activated)
`spec.skills.custom[]` entries are materialized into `/opt/data/skills/<name>/`
(from a ConfigMap/Secret ref or inline `SKILL.md`) by an init step, and kept out
of `disabled` ⇒ active. The skill is a `SKILL.md` with the verified frontmatter
schema (`name`, `description`, `version`, `platforms`, `metadata.hermes.tags`).

### 6.3 The `package-management` skill (added & activated automatically)
`spec.skills.enablePackageManagementSkill: true` (default) ships a built-in
`package-management` SKILL.md (embedded in the operator/agent image) that teaches
the agent to install tooling **without sudo** using the on-PATH `brew` (persisted)
and, where root is available at boot, to declare apt packages via the CR. It is
written to `/opt/data/skills/package-management/` and auto-enabled. This directly
satisfies "a skill for this should be added and activated automatically."

---

## 7. Reconciliation

Controller-runtime manager; one controller owns the child objects.

```
Reconcile(req):
  1. Fetch HermesAgent (not-found -> owner-ref GC).
  2. Resolve presetRef; deep-merge preset -> spec.
  3. Render config.yaml (typed sections incl. skills.disabled/platform_disabled
     + deep-merged extraConfig), SOUL.md, and skill payloads (custom + built-in
     package-management) into ConfigMaps.
  4. configHash = sha256(config.yaml || skill payloads || packages || sorted[Secret name+rv]).
  5. Server-side apply (owner refs set):
       ServiceAccount + RoleBinding (if serviceAccount.create; SA annotations
                   for workload identity; binds the reloader Role — 3.7/10)
       PVC        (one shared claim, size from spec; never destructive-resize)
       ConfigMaps (config.yaml+SOUL.md; per-skill SKILL.md payloads)
       Service    (8642 if apiServer; 9119 if dashboard; channel webhook ports;
                   type+annotations per surface service: block)
       Ingress    (one per surface with ingress.enabled; className/host/path/
                   tls + custom annotations merged verbatim; owner-ref'd)
       Deployment (replicas 0/1; Recreate; pod template built as:
                   a) base = hermes container + reloader sidecar (8),
                      shared PVC -> /opt/data (subPath data) + /opt/data/.local (subPath dotlocal) + /home/linuxbrew/.linuxbrew (subPath linuxbrew),
                      ConfigMap subPath -> /opt/data/{config.yaml,SOUL.md},
                      skill ConfigMaps staged for reloader init, emptyDir -> /dev/shm,
                      serviceAccountName per 3.7, env/envFrom, HERMES_UID/GID, runAsRoot/fsGroup, probes per 1.2,
                      annotation hermes.io/config-hash;
                   a') if terminalBackend == docker (11.2): inject dind sidecar
                      (privileged or rootless), shared socket emptyDir -> /var/run/dind,
                      SAME PVC subPath mounts at identical paths in dind +
                      /var/lib/docker (subPath dind), DOCKER_HOST on hermes container;
                   b) strategic-merge spec.podTemplate OVER the base (3.6);
                   c) re-assert invariants (shared-PVC mounts, dind sidecar+mounts,
                      config-hash, root-start) — operator wins.)
  6. status: phase, conditions, endpoints, skills{}, packages{}.
```

---

## 8. Executor / reloader (in-pod)

The brief's "executor/reloader … to apply configuration and changes from the k8s
documents." Split of responsibility:

- **Operator (cluster):** k8s-level desired state — Deployment/PVC/ConfigMap/
  Service, restart-vs-hot-reload decision, status.
- **Reloader (in-pod sidecar, `hermes-reloader` image §9):** applies changes that
  must run *inside* the container against the shared volume, as the `hermes`
  user (and a small root init step where needed):
  1. **Brew prefix init** + `brew install` for `packages.brew` (idempotent, §5.1).
  2. **Skill materialization** — copy `custom`/`package-management` skill payloads
     into `/opt/data/skills/...`; run `skills_sync.py`.
  3. **Skill enablement** — apply `disabled`/`platform_disabled` via the
     `skills_config` module (`hermes skills config`).
  4. **Config hot-reload** — for `model.context_length` / `compression.*`,
     update the on-volume `config.yaml` and signal the gateway (avoiding a full
     restart) — v1beta1; v1 falls back to operator-driven `Recreate`.
  5. **Gateway restart** — invoke `hermes gateway restart` (gateway/restart.py)
     when a reloadable-in-place path is unavailable but a full pod restart is not
     desired. It must respect `gateway.lock` (never spawns a competing gateway).
- apt install runs in the **root stage of the entrypoint** (pre-gosu), not the
  sidecar (the sidecar runs unprivileged).

The reloader watches its mounted config/skill ConfigMaps (and optionally the CR
via the k8s API with a scoped RBAC) and reconciles the in-container state on
change, reporting back through a shared status file the operator reads into
`status.skills`/`status.packages`.

---

## 9. Components, images & registry

Three components, each with its own Dockerfile, built and **pushed to
`harbor.bne1.ouchi.com.au/applications/`** on build.

| Component | Image | Contents |
| --- | --- | --- |
| **Operator** | `harbor.bne1.ouchi.com.au/applications/hermes-operator` | Go controller-manager (distroless/static). |
| **Agent** | `harbor.bne1.ouchi.com.au/applications/hermes-agent` | `FROM nousresearch/hermes-agent:v2026.5.16` + Homebrew dist at `/opt/homebrew-dist` + HOMEBREW_* env + extended entrypoint (brew init, apt apply) + embedded `package-management` skill. |
| **Reloader** | `harbor.bne1.ouchi.com.au/applications/hermes-reloader` | Small Go (or Python) binary; runs as in-pod sidecar (§8). May reuse the agent image to get `hermes`/`brew` on PATH, or a slim image that execs into the agent container. |

### 9.1 Build & push
- `make docker-build docker-push IMG_REGISTRY=harbor.bne1.ouchi.com.au/applications`
  builds/pushes all three with a shared version tag (default = operator release;
  agent also tagged with the upstream `v2026.5.16` it derives from).
- Multi-arch via `docker buildx` (`linux/amd64`, `linux/arm64`).
- Requires Harbor credentials (`docker login harbor.bne1.ouchi.com.au`); CI uses
  a robot account. Image pull in-cluster uses `imagePullSecrets` (`harbor-pull`).
- `config/` (kustomize) defaults `image:` to the Harbor agent image.

### 9.2 Project layout
```
hermes-operator/
├── api/v1alpha1/                 # types + deepcopy + CEL
├── internal/{controller,config,resources}/
├── cmd/{operator,reloader}/main.go
├── images/
│   ├── operator/Dockerfile
│   ├── agent/Dockerfile          # derives upstream + brew + skill + entrypoint
│   ├── agent/entrypoint.d/       # brew-init.sh, apt-apply.sh hooks
│   ├── agent/skills/package-management/SKILL.md
│   └── reloader/Dockerfile
├── config/                       # kustomize: crd, rbac, manager, samples
├── charts/
│   ├── hermes-operator/          # installs the operator (CRDs + RBAC + manager)
│   └── hermes-agent/             # renders a HermesAgent CR (+ Secret) for app teams
├── third_party/hermes-agent/     # submodule @ v2026.5.16
├── Makefile · docs/{brief,specification}.md
```

### 9.3 Helm charts

Two charts are published as **OCI artifacts** to the Harbor `helm` project at
`oci://harbor.bne1.ouchi.com.au/helm` (images live separately under
`/applications` — §9), versioned with the same release tag.

#### a) `hermes-operator` (cluster-scoped install)
Installs the operator itself: CRDs, RBAC, the controller-manager Deployment,
Service/metrics, and the `harbor-pull` `imagePullSecret`.

```yaml
# values.yaml (operator chart)
image:
  repository: harbor.bne1.ouchi.com.au/applications/hermes-operator
  tag: ""                       # defaults to .Chart.AppVersion
  pullPolicy: IfNotPresent
imagePullSecrets: [{ name: harbor-pull }]
crds:
  install: true                 # install/upgrade CRDs with the chart
  keep: true                    # annotate "helm.sh/resource-policy: keep" (don't delete CRs on uninstall)
rbac: { create: true }
serviceAccount: { create: true, name: "" }
replicas: 1
resources:
  requests: { cpu: 100m, memory: 128Mi }
  limits:   { cpu: 500m, memory: 256Mi }
metrics: { enabled: true, serviceMonitor: false }
leaderElection: { enabled: true }
podTemplate: {}                 # operator pod overlay (nodeSelector/tolerations/…)
watchNamespaces: []             # [] = all namespaces (cluster-scoped); or restrict
```

CRD lifecycle: templated under `crds/` and guarded by `crds.install`. With
Helm's CRD-handling caveats in mind, the chart installs CRDs as regular
templates (not the un-upgradable `crds/` special dir) annotated
`helm.sh/resource-policy: keep` so upgrades work and uninstall never orphans live
`HermesAgent`s.

#### b) `hermes-agent` (per-instance, namespaced)
A thin chart whose `values.yaml` mirrors `HermesAgent.spec`; it renders one
`HermesAgent` CR (and optionally a `Secret` for provider keys/tokens). App teams
deploy agents without writing raw CRs.

```yaml
# values.yaml (agent chart) — keys map 1:1 to HermesAgent.spec
fullnameOverride: ""
image: harbor.bne1.ouchi.com.au/applications/hermes-agent:v2026.5.16
model: { default: anthropic/claude-opus-4.6, provider: auto }
storage: { size: 20Gi, storageClassName: "", reclaimPolicy: Retain }
apiServer:
  enabled: false
  service: { type: ClusterIP, annotations: {} }
  ingress:
    enabled: false
    className: nginx
    host: ""
    annotations: {}
    tls: []
dashboard: { enabled: false, ingress: { enabled: false } }
serviceAccount:
  create: true
  name: ""
  annotations: {}               # IRSA / GKE / Azure workload identity
channels: []
skills:
  disabled: []
  custom: []
  enablePackageManagementSkill: true
packages: { apt: [], brew: [] }
podTemplate: {}                 # full pod overlay (3.6)
# Secret creation (optional). When create=true the chart makes the Secret the
# CR's env/secret refs point at; otherwise reference an existing Secret.
secret:
  create: false
  name: ""                      # existing Secret name when create=false
  data: {}                      # k:v rendered into a Secret (stringData); prefer --set-file / existing Secret
```

Both charts ship `Chart.yaml` (with `appVersion` = release), a README, and
`helm lint` + `helm template` checks in CI. `make helm-package helm-push`
packages and pushes to the Harbor OCI path.

> Helm is **one** distribution path; raw `kustomize` (`config/`) remains
> supported. The operator chart is the recommended install; the agent chart is
> optional sugar over the CRD.

---

## 10. Watches & RBAC

**Watches:** primary `HermesAgent`; owned `Deployment`/`Service`/`Ingress`/
`ConfigMap`/`PVC`; referenced `Secret`s/`HermesConfigPreset`s mapped to
dependents via field indexes.

**Operator ClusterRole:** full on `hermesagents`/`hermesconfigpresets`/
`hermeschannels` (+`/status`,`/finalizers`); `apps:deployments` CRUD;
`networking.k8s.io:ingresses` CRUD; core
`services`/`configmaps`/`persistentvolumeclaims`/`serviceaccounts`/`events`;
`rbac.authorization.k8s.io:roles`/`rolebindings` **CRUD** (create/manage the
per-agent reloader `Role` + `RoleBinding`); `pods` get/list/watch; `secrets`
**get/list/watch only**.

> **RoleBinding is explicitly allowed.** Kubernetes' escalation-prevention means
> the operator may only create a `RoleBinding`/`Role` if it either holds the
> `bind`/`escalate` verb on the target Role **or** already possesses every
> permission that Role grants (RBAC API §"restrictions on creating"). Since the
> reloader Role is a strict subset of the operator's own ClusterRole (read CR +
> named ConfigMaps, write `/status`), the operator satisfies the "holds the
> granted permissions" path — no separate `bind`/`escalate` grant is required.
> If the reloader Role is ever widened beyond the operator's permissions, add an
> explicit `bind` verb on that Role to the operator ClusterRole.

**Per-agent ServiceAccount (§3.7):** created by the operator when
`serviceAccount.create: true` (else an existing SA is used). The agent pod +
reloader sidecar run as it; SA annotations carry cloud workload-identity.

**Reloader Role (namespaced, scoped, bound to the agent SA):** read its own CR +
named ConfigMaps; write the owning `HermesAgent` `/status` (skills/packages
sub-status); no secret-mutation. Mounts the shared PVC.

---

## 11. Code execution & tool sandboxing

Hermes is an **agent that runs code and shell tools**; `terminal.backend` selects
*where* those run (`local|docker|ssh|modal|daytona|vercel_sandbox|singularity`,
default `local`), and `code_execution.*` bounds them (`timeout`,
`max_tool_calls`). This is the reason brew/apt/`~/.local` matter (§5) — tools
execute in the agent container.

### 11.1 `local` backend (default, recommended in-cluster)
Tool/shell commands run **inside the agent container** as the `hermes` user
(post-gosu). Implications the operator owns:
- The pod **is** the sandbox — size `resources` and `shmSize` for the workloads
  the agent will run; the RWO volume holds anything it writes under `~/`.
- Blast radius = the pod's ServiceAccount (§3.7), mounted Secrets (env), and
  network egress. Recommend a tight SA, `automountToken: false` when the agent
  needs no API access, and a NetworkPolicy (§15 open item) to bound egress to
  the provider/messaging endpoints it actually needs.
- `runtime.codeExecution.timeout`/`maxToolCalls` cap runaway tool loops; surface
  them so an agent can't peg the node.

### 11.2 `docker` backend → injected DinD sidecar
When `runtime.terminalBackend: docker`, the agent spawns **sibling containers**.
The operator handles this by **injecting a Docker-in-Docker (DinD) sidecar** into
the same pod and wiring the agent to it — the agent never touches the node's
Docker/CRI socket.

**What the operator adds (automatically, when backend == docker):**
1. A **`dind` sidecar** from `runtime.docker.image` (default `docker:27-dind`)
   running the Docker daemon. `privileged: true` for standard DinD, or
   `rootless: true` ⇒ `docker:27-dind-rootless` (no privileged, weaker isolation
   trade-offs).
2. **`DOCKER_HOST` on the agent container** pointing at that daemon:
   - `socketTransport: unix` (default) — a shared **emptyDir** mounted at
     `/var/run/dind` in both containers; daemon listens on
     `unix:///var/run/dind/docker.sock`, agent sets
     `DOCKER_HOST=unix:///var/run/dind/docker.sock`.
   - `socketTransport: tcp` — daemon on `tcp://127.0.0.1:2375` (intra-pod
     localhost), agent `DOCKER_HOST=tcp://127.0.0.1:2375`.
3. **The same shared-PVC subPath mounts, at IDENTICAL paths, in the dind
   container** (`/opt/data`, `/opt/data/.local`, `/home/linuxbrew/.linuxbrew` —
   §4.1). This is the crux: when the agent runs `docker run -v /opt/data/foo:…`,
   the **dind daemon** resolves the bind-mount *source* path on **its own**
   filesystem, not the agent's. Mapping the identical PVC subPaths into dind
   makes those host-path bind mounts resolve to the same data the agent sees —
   without this, bind mounts silently point at empty/missing paths.
4. A dedicated **`/var/lib/docker`** for the daemon's image/layer store — a
   separate `subPath` (`dind`) on the **same shared PVC** sized by
   `runtime.docker.storage.size` (keeps the single-PVC invariant, §4). Use an
   `emptyDir` instead only if persistence of pulled images isn't wanted.

**Posture:** standard DinD is privileged and **breaks `restricted` PSS** (§11.3);
the validator warns and the namespace must allow privileged (or use `rootless`).
For stronger isolation without privilege, prefer a **remote** backend
(`modal`/`daytona`/`vercel_sandbox`) via `extraConfig` + Secret — execution moves
off-cluster entirely. The dind sidecar is **operator-managed** and re-asserted
after any `podTemplate` overlay (§3.6).

### 11.3 Sandboxing knobs
- Prefer `runtimeClassName: gvisor`/`kata` (via `podTemplate`, §3.6) for kernel
  isolation when running `local`.
- `local` is incompatible with a read-only root filesystem and with restricted
  Pod Security Standards that forbid root-start (we need root for the entrypoint
  gosu/apt step, §4.2); document the **baseline** PSS as the supported profile,
  and note that `runAsRoot: false` + no-apt is the path toward `restricted`.

---

## 12. Scheduled tasks (cron)

Hermes has a first-class scheduler: jobs persist in **`/opt/data/cron/jobs.json`**
(not `config.yaml`), with a lock under `/opt/data/cron`, dispatched by the
in-gateway scheduler. Two facts shape the operator model:

1. **Cron is stateful, agent-mutable.** The agent (and `hermes cron` CLI) can
   create/delete jobs at runtime. The operator therefore *reconciles a declared
   subset*, it does not own the whole file.
2. **The scheduler runs in the single gateway.** No extra workload/CronJob is
   created — jobs fire inside the one pod, so they inherit the singleton and the
   restart model (a job mid-run is interrupted by a `Recreate`; jobs are
   re-evaluated on boot).

`spec.cron.jobs[]` (name/schedule/prompt) are **seeded/reconciled by the reloader**
(§8) into `jobs.json`, keyed by `name`. `pruneUnmanaged: false` (default) leaves
agent-created jobs alone; `true` makes the declared set authoritative (operator
removes unknown jobs) — useful for fully-GitOps'd agents. Job execution status is
the agent's concern (its logs/sessions), not mirrored into CR status in v1.

> We deliberately do **not** model these as Kubernetes `CronJob`s — that would
> spawn competing processes against the shared volume and violate single-writer
> (§1.1). All scheduling stays inside the gateway.

---

## 13. Finalizers, shutdown & deletion

### 13.1 Finalizer
The operator sets a finalizer `hermes.nousresearch.io/cleanup` on each
`HermesAgent`. On delete it runs an ordered teardown before removing the
finalizer:
1. Scale the Deployment to 0 and **wait for pod termination** so `gateway.lock`
   (flock on the RWO volume, §1.1) is released and no writer remains.
2. Honor `storage.reclaimPolicy`: `Retain` (default) detaches the PVC and leaves
   it; `Delete` removes the PVC (and its data) only here, after the writer is
   gone.
3. Owner-ref'd children (Deployment/Service/Ingress/ConfigMaps/SA/RoleBinding)
   are GC'd by Kubernetes; the finalizer only gates the PVC + lock ordering.

### 13.2 Graceful gateway shutdown
- The pod template sets a **`terminationGracePeriodSeconds`** (default ~60,
  overridable via `podTemplate`) so the gateway can flush sessions/memory and
  release adapters; `tini` (PID 1) forwards `SIGTERM` to the gateway.
- `preStop` hook (optional) runs `hermes gateway stop` for a clean adapter
  disconnect before SIGTERM, avoiding half-delivered messages.
- On `Recreate` rollout the same ordering applies: old pod drains and releases
  the lock **before** the new pod starts (this is why `Recreate`, not
  RollingUpdate — §1.1/§5).

### 13.3 Pause semantics
`replicas: 0` (Paused, §5) is the non-destructive stop: lock released, volume
retained, no finalizer interaction. Distinct from deletion.

---

## 14. Upgrades & versioning

### 14.1 Agent image upgrades + config migration
Bumping `spec.image` to a newer upstream Hermes release can change the config
schema — upstream tracks **`_config_version`** (23 at the pinned tag) and ships
`migrate_config()` / `hermes config migrate`. The operator's contract:
- The operator renders a *typed subset* of `config.yaml`; unknown/new keys come
  from `extraConfig` or upstream defaults, so most bumps are transparent.
- On image change, the reloader runs **`hermes config migrate`** against the
  on-volume `config.yaml` (it's the agent's own, version-matched migrator) before
  starting the gateway, then the gateway boots on the migrated file. This keeps
  migration logic with the binary that owns it rather than reimplementing it in
  Go.
- `status` records the running agent image + observed `_config_version`; a
  `Degraded` condition with reason `ConfigVersionAhead` is set if the on-volume
  config is newer than the image (downgrade guard).

### 14.2 Operator CRD versioning
- API starts at `v1alpha1`. Moving to `v1beta1`/`v1` uses a **conversion webhook**
  (listed v1beta1, §16) with a hub-and-spoke model; stored version is pinned and
  CRs are converted on read. Additive fields stay within a version (no webhook
  needed); breaking shape changes trigger a new version.
- The `hermes-operator` Helm chart (§9.3a) installs CRDs annotated
  `helm.sh/resource-policy: keep` so operator upgrades never drop CRDs or orphan
  live agents.

### 14.3 Operator ↔ agent skew
The operator pins a **tested upstream tag** as the default `spec.image` (the
`third_party/hermes-agent` submodule, currently `v2026.5.16`) and runs
config-rendering tests against that exact `cli-config.yaml.example`. Users may
override `spec.image` to a different tag; the validator warns when the image tag
differs from the operator's tested baseline (skew is allowed but flagged), and
the §11/§5 behaviors (ports, paths, `_config_version`) should be re-verified per
the submodule-bump checklist at the top of this doc.

---

## 15. Open questions / to validate

1. **`hermes gateway status` exit code** — confirm non-zero when unhealthy (exec
   probe validity).
2. **`subPath` config.yaml chmod** — confirm entrypoint's root-stage `chmod 640`
   on a read-only subPath mount is non-fatal; else use an initContainer copy.
3. **Homebrew on PVC subPath at default prefix** — validate bottle installs work
   with the prefix populated from `/opt/homebrew-dist` on first boot and owned by
   gid 10000; confirm `git`-based `brew update` works on the volume.
4. **apt at every boot** — acceptable startup latency / mirror egress? Consider a
   "bake a derived image" recommendation for large `apt` lists.
5. **`hermes skills config` non-interactive surface** — confirm a scriptable path
   for the reloader to set `disabled`/`platform_disabled` without a TTY.
6. **Reloader vs single-writer** — confirm `hermes gateway restart` and skill/
   config CLIs don't acquire a competing `gateway.lock` while the gateway runs.
7. **Metrics / log surfacing** — Prometheus endpoint? logs to `/opt/data/logs`
   vs container stdout (`PYTHONUNBUFFERED=1`).
8. **`hermes config migrate` non-interactive** — confirm `migrate_config()` runs
   headless and is safe to invoke from the reloader on every boot (§14.1).
9. **`jobs.json` schema + `hermes cron` headless** — confirm the on-disk job
   format and a non-interactive way for the reloader to seed/prune jobs (§12).
10. **DinD daemon readiness ordering** — ensure the agent waits for the dind
    daemon socket before issuing docker commands (startup probe on the dind
    sidecar, or the reloader gating gateway start on `docker info`); confirm
    rootless DinD works without privileged in the target cluster (§11.2).

---

## 16. Roadmap

| Phase | Scope |
| --- | --- |
| **v1alpha1 (MVP)** | `HermesAgent`; single shared PVC (subPath mounts incl. `~/.local`, settable size); Deployment(Recreate)+ConfigMap+Service+Ingress; `podTemplate` overlay; serviceAccount; secret refs; config render; **skill activation (defaults + custom + package-management)**; **apt(boot)+brew(PVC) packages**; `local` code-exec backend (§11); **finalizer + graceful shutdown** (§13); **config-migrate on image bump** (§14.1); exec/http probes; reloader sidecar; per-component Dockerfiles → Harbor; **`hermes-operator` Helm chart** (9.3a). |
| **v1beta1** | `HermesConfigPreset`+`HermesChannel`; admission + **conversion webhook** (§14.2); declarative **cron jobs** (§12); **`docker` backend via injected DinD sidecar** (§11.2); in-place hot reload via reloader; metrics/`ServiceMonitor`; log sidecar; NetworkPolicy; **`hermes-agent` Helm chart** (9.3b). |
| **v1** | Backup/restore (VolumeSnapshot, quiesced single-writer); multi-profile; rootless-DinD hardening + remote terminal backends (`modal`/`daytona`/`vercel_sandbox`) (§11.2). |

---

## 17. Acceptance criteria (MVP)

1. Applying a `HermesAgent` with a model + provider Secret yields a running pod
   that passes its probe and reports `status.phase: Running`.
2. **One PVC** of `spec.storage.size` is created and mounted at `/opt/data`,
   `/opt/data/.local`, and `/home/linuxbrew/.linuxbrew` (subPaths `data`,
   `dotlocal`, `linuxbrew` of the same claim); no second claim exists.
3. `config.yaml` matches typed fields + `extraConfig`, `${VAR}` intact, owned by
   UID 10000; `skills.disabled` reflects `spec.skills.disabled`.
4. All bundled default skills are present in `/opt/data/skills` and active when
   `spec.skills.disabled` is empty.
5. The `package-management` skill is present in `/opt/data/skills` and enabled by
   default (`enablePackageManagementSkill: true`).
6. `spec.packages.brew: [gh]` ⇒ `gh` installed under
   `/home/linuxbrew/.linuxbrew` **without sudo**, on PATH, and **persists** across
   a pod restart (no reinstall). `~/.local/bin` is on PATH.
7. `spec.packages.apt: [jq]` ⇒ `jq` available at runtime (re-applied each boot);
   skipped with a clear condition when `runAsRoot: false`.
8. Editing a restart-required field rolls the pod (stop-then-start); the old pod
   releases the PVC + `gateway.lock` before the new pod starts.
9. `replicas: 2` (or >1) is rejected by validation.
10. `make docker-build docker-push` produces and pushes
    `hermes-operator`, `hermes-agent`, `hermes-reloader` to
    `harbor.bne1.ouchi.com.au/applications/`.
11. `apiServer.ingress.enabled: true` with custom `annotations`/`host`/`tls`
    produces an `Ingress` carrying those annotations verbatim, routing to the
    agent Service; `service.type: LoadBalancer` + annotations are reflected on
    the Service.
12. A `spec.podTemplate` overlay adding a sidecar container, a `nodeSelector`, a
    `toleration`, and a pod label appears on the running pod, **while** the
    shared-PVC mounts, `config-hash` annotation, and root-start are preserved; an
    overlay that remaps `/opt/data` or sets `replicas` is rejected.
13. `helm install` of the `hermes-operator` chart from
    `oci://harbor.bne1.ouchi.com.au/helm` installs CRDs + RBAC +
    manager; `helm uninstall` leaves existing `HermesAgent` CRs intact (CRDs
    annotated keep). `helm lint` passes for both charts in CI. `make helm-push`
    pushes both charts to `oci://harbor.bne1.ouchi.com.au/helm`.
14. `serviceAccount.create: true` ⇒ the pod runs under an operator-created SA
    bound to the reloader Role, carrying the configured workload-identity
    annotations; `create: false` with a `name` uses that existing SA unchanged.
    A `podTemplate.spec.serviceAccountName` conflicting with `spec.serviceAccount`
    is rejected.
15. `runtime.terminalBackend: docker` ⇒ the pod gains a dind sidecar; the agent's
    `DOCKER_HOST` reaches it; `docker run -v /opt/data/<x>:…` inside the agent
    bind-mounts the **same** data the agent sees (verified: the path exists in
    dind because the shared PVC subPaths are mounted at identical paths). The
    dind image store is on the `dind` subPath of the same PVC; no second PVC.
