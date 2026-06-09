# Derived Hermes Agent image: upstream + Homebrew (persisted on the PVC at
# runtime) + the package-management skill + an s6-overlay cont-init hook that
# installs declared apt packages as root before the supervised services start.
#
# Upstream (>= v2026.5.x) boots via s6-overlay: ENTRYPOINT is
# `/init /opt/hermes/docker/main-wrapper.sh` (PID 1 = s6-svscan). We do NOT
# override that ENTRYPOINT — we only add a cont-init.d hook for the root step.
#
# Build context is the repository root:
#   docker build -f images/agent/Dockerfile .
ARG UPSTREAM_IMAGE=nousresearch/hermes-agent:v2026.6.5
FROM ${UPSTREAM_IMAGE}

USER root

# Homebrew environment. The prefix is the DEFAULT linuxbrew path so prebuilt
# bottles work; at runtime it is the shared-PVC mount (spec §5.1). PATH also
# already contains /opt/data/.local/bin from the upstream image.
ENV HOMEBREW_PREFIX=/home/linuxbrew/.linuxbrew \
    HOMEBREW_CELLAR=/home/linuxbrew/.linuxbrew/Cellar \
    HOMEBREW_REPOSITORY=/home/linuxbrew/.linuxbrew/Homebrew \
    HOMEBREW_NO_ANALYTICS=1
ENV PATH="/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:${PATH}"

# Restore the Homebrew prefix and the pip user-site bin (~/.local/bin) on PATH for
# LOGIN shells. The ENV above covers the gateway process and non-login subprocesses,
# but hermes' local terminal backend runs commands via `bash -l`, which re-sources
# /etc/profile and resets PATH — dropping both, so brew tools (kubectl, jq, …) and
# pip console-scripts aren't found. This profile.d snippet (where Homebrew's
# installer normally puts shellenv) restores them; each entry is dir-guarded
# (no-op before the PVC prefix is populated) and de-duplicated.
RUN printf '%s\n' \
    '# Hermes/Homebrew/pip bin dirs on PATH for login shells (local terminal runs `bash -l`).' \
    'for _d in /opt/data/.local/bin /home/linuxbrew/.linuxbrew/sbin /home/linuxbrew/.linuxbrew/bin; do' \
    '  case ":$PATH:" in *:"$_d":*) ;; *) PATH="$_d:$PATH" ;; esac' \
    'done' \
    'export PATH' \
    '[ -d /home/linuxbrew/.linuxbrew/bin ] && export HOMEBREW_PREFIX=/home/linuxbrew/.linuxbrew' \
    > /etc/profile.d/hermes-path.sh && chmod 0644 /etc/profile.d/hermes-path.sh

# Bake a Homebrew distribution at an UNMOUNTED path so the PVC overlay at
# /home/linuxbrew/.linuxbrew does not mask it. The reloader sidecar copies this
# into the prefix on first boot (idempotent), as the hermes user, no sudo.
RUN git clone --depth=1 https://github.com/Homebrew/brew /opt/homebrew-dist \
    && rm -rf /opt/homebrew-dist/.git \
    && chown -R 10000:10000 /opt/homebrew-dist

# Embed the auto-activated package-management skill into the bundled skills dir
# so skills_sync.py copies it to /opt/data/skills on boot (spec §6.3).
COPY images/agent/skills/package-management /opt/hermes/skills/package-management
RUN chown -R 10000:10000 /opt/hermes/skills/package-management

# Root-only apt install, wired as an s6-overlay cont-init hook. /init runs
# /etc/cont-init.d/* as root during stage 2 (before user services), which is
# the hook point the pre-s6 entrypoint wrapper used to occupy. Named 00- so it
# runs ahead of upstream's 01-hermes-setup. See images/agent/cont-init.d/00-hermes-apt.
COPY --chmod=0755 images/agent/cont-init.d/00-hermes-apt /etc/cont-init.d/00-hermes-apt

# No ENTRYPOINT/CMD override: inherit upstream's s6-overlay
# `/init /opt/hermes/docker/main-wrapper.sh`, which runs cont-init.d (incl. the
# hook above), starts the supervised services, then exec's hermes as UID 10000
# via `s6-setuidgid hermes`.
