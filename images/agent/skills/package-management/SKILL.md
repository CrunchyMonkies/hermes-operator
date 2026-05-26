---
name: package-management
description: Install CLI tools and packages at runtime without sudo using Homebrew (persisted on the data volume) or, where declared on the HermesAgent CR, apt. Use when a task needs a tool that is not already installed.
version: 0.1.0
platforms:
  - linux
metadata:
  hermes:
    tags: [packages, brew, apt, tooling, devops, installation]
---

# Package management

You run inside a Kubernetes-managed Hermes gateway. You can install extra
command-line tools yourself. There are two mechanisms; prefer **brew**.

## Homebrew (preferred — no sudo, persisted)

Homebrew is installed and on your `PATH`. Its prefix
(`/home/linuxbrew/.linuxbrew`) lives on the persistent data volume, so anything
you install with it **survives restarts** and needs **no sudo**.

```bash
brew install <formula>      # e.g. brew install jq ripgrep fd gh
brew list                   # what's already installed
which <tool>                # confirm it's on PATH
```

Tools installed via `pip --user`, `npm -g`, or `uv tool` also persist, because
`~/.local` (`/opt/data/.local`) is on the volume and `~/.local/bin` is on
`PATH`.

## Declared packages (operator-managed)

The operator can pre-install packages for every boot when they are declared on
the `HermesAgent` custom resource:

- `spec.packages.brew: [ ... ]` — installed to the persistent Homebrew prefix by
  the reloader sidecar (persisted; no reinstall on restart).
- `spec.packages.apt: [ ... ]` — installed at container start as root. These are
  system-level and are **re-applied on every restart** (not persisted), so they
  add startup time. For heavy or stable system dependencies, ask an operator to
  bake them into a derived image instead.

If you need a tool permanently for this agent, recommend adding it to
`spec.packages.brew` (persisted) rather than installing ad hoc.

## Guidance

1. Check if the tool already exists (`which <tool>`) before installing.
2. Use `brew install` for one-off needs — it is fast (bottles) and persistent.
3. Do not attempt `sudo` or `apt-get` yourself; you are not root at runtime.
   System packages must come through `spec.packages.apt` on the CR.
