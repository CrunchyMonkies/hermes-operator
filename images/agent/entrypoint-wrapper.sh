#!/bin/bash
# Derived-image entrypoint wrapper.
#
# The upstream entrypoint (docker/entrypoint.sh) runs its root stage and then
# re-execs itself via `gosu hermes`, so there is no hook point left for a
# root-only step afterwards. We therefore run the root-only apt install here,
# BEFORE delegating to the upstream entrypoint (which still handles usermod,
# volume chown, config/skill seeding, skills_sync, dashboard, and the gosu drop).
#
# apt packages are system-level and live OUTSIDE the data volume, so they are
# re-applied on every pod start (spec §5.3). Homebrew prefix init + `brew
# install` happen in the reloader sidecar as the hermes user (spec §5.1, §8).
set -e

INSTALL_DIR="/opt/hermes"

# Root stage: install declared apt packages. Requires root (runAsRoot: true).
if [ "$(id -u)" = "0" ] && [ -n "$HERMES_APT_PACKAGES" ]; then
    echo "[entrypoint-wrapper] Installing apt packages: $HERMES_APT_PACKAGES"
    apt-get update
    # shellcheck disable=SC2086
    apt-get install -y --no-install-recommends $HERMES_APT_PACKAGES
    rm -rf /var/lib/apt/lists/*
fi

# Delegate to the upstream entrypoint unchanged.
exec "${INSTALL_DIR}/docker/entrypoint.sh" "$@"
