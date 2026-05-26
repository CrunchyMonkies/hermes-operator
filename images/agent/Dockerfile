# Derived Hermes Agent image: upstream + Homebrew (persisted on the PVC at
# runtime) + the package-management skill + a wrapper entrypoint that installs
# declared apt packages as root before delegating to the upstream entrypoint.
#
# Build context is the repository root:
#   docker build -f images/agent/Dockerfile .
ARG UPSTREAM_IMAGE=nousresearch/hermes-agent:v2026.5.16
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

# Bake a Homebrew distribution at an UNMOUNTED path so the PVC overlay at
# /home/linuxbrew/.linuxbrew does not mask it. The reloader sidecar copies this
# into the prefix on first boot (idempotent), as the hermes user, no sudo.
RUN git clone --depth=1 https://github.com/Homebrew/brew /opt/homebrew-dist \
    && rm -rf /opt/homebrew-dist/.git \
    && chown -R 10000:10000 /opt/homebrew-dist

# Embed the auto-activated package-management skill into the bundled skills dir
# so skills_sync.py copies it to /opt/data/skills on boot (spec §6.3).
COPY images/agent/skills/package-management /opt/hermes/skills/package-management

# Install the wrapper entrypoint.
COPY images/agent/entrypoint-wrapper.sh /opt/hermes/docker/entrypoint-wrapper.sh
RUN chmod 0755 /opt/hermes/docker/entrypoint-wrapper.sh \
    && chown -R 10000:10000 /opt/hermes/skills/package-management

ENTRYPOINT [ "/usr/bin/tini", "-g", "--", "/opt/hermes/docker/entrypoint-wrapper.sh" ]
