# hermes-operator (Helm chart)

Installs the **Hermes Operator** — CRDs, RBAC, and the controller-manager that
reconciles `HermesAgent` resources into Hermes Agent gateway Deployments.

## Install

```bash
helm install hermes-operator \
  oci://harbor.bne1.ouchi.com.au/helm/hermes-operator \
  --namespace hermes-system --create-namespace
```

## CRD lifecycle

CRDs are installed as regular chart templates (under `templates/`, sourced from
`files/crds/`) annotated `helm.sh/resource-policy: keep`. This means:

- `helm upgrade` re-applies CRD schema changes (unlike Helm's special `crds/`
  dir, which never upgrades).
- `helm uninstall` **leaves existing `HermesAgent` CRs and the CRDs intact** —
  your running agents are not deleted.

Set `crds.install=false` to manage CRDs out-of-band.

## Key values

| Key | Default | Purpose |
| --- | --- | --- |
| `image.repository` / `image.tag` | `…/hermes-operator` / chart appVersion | Operator image |
| `reloaderImage.repository` / `.tag` | `…/hermes-reloader` / chart appVersion | Sidecar image injected into agent pods (via `RELOADER_IMAGE`) |
| `imagePullSecrets` | `[{name: harbor-pull}]` | Harbor pull secret |
| `crds.install` | `true` | Install/upgrade CRDs with the chart |
| `rbac.create` | `true` | Create the operator ClusterRole/Binding |
| `serviceAccount.create` / `.name` | `true` / `""` | Operator SA |
| `metrics.enabled` | `true` | Expose the metrics service |
| `leaderElection.enabled` | `true` | Leader election (+ lease RBAC) |
| `podTemplate` | `{}` | Operator pod overlay (nodeSelector/tolerations/…) |

See `docs/specification.md` §9.3a for the full design.
