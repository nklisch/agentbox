#!/usr/bin/env bash
# containers kit installer: podman, buildah, skopeo, podman-compose, docker-compose,
# fuse-overlayfs, slirp4netns; /usr/local/bin/docker shim; containers storage config.
# Verified against upstream release APIs on 2026-05-05.
set -euo pipefail

# --- Pinned versions (env-var overridable) ---
PODMAN_COMPOSE_VERSION="${PODMAN_COMPOSE_VERSION:-1.5.0}"   # pip: verified 2026-05-05
DOCKER_COMPOSE_VERSION="${DOCKER_COMPOSE_VERSION:-5.1.3}"   # github.com/docker/compose: verified 2026-05-05

# --- Arch detection ---
# docker/compose ships binaries named with the raw uname -m suffix
# (docker-compose-linux-x86_64, docker-compose-linux-aarch64) — NOT the
# Go-style amd64/arm64 names. Use ARCH directly. The earlier GO_ARCH
# alias was wrong and 404'd both arches; verified 2026-05-07 against the
# v5.1.3 release asset list.
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|aarch64) ;;
  *)
    echo "unsupported arch: $ARCH" >&2
    exit 1
    ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- Wire env.d into zsh (idempotent; base kit does this once for everyone) ---
# The base kit's install.sh writes the env.d sourcing block to /etc/zsh/zshenv.
# We skip it here to avoid duplication — base always runs first (manifest.toml
# declares depends_on = ["base"]).

# --- podman-compose (via pip3) ---
# pip3 on Debian Bookworm enforces PEP 668 (system-wide pip installs require
# --break-system-packages). No apt package for podman-compose on Bookworm.
echo "Installing podman-compose ${PODMAN_COMPOSE_VERSION}..."
pip3 install --break-system-packages --no-cache-dir "podman-compose==${PODMAN_COMPOSE_VERSION}"

# --- docker-compose (standalone Go binary from GitHub releases) ---
# This is the standalone docker-compose v2 binary (distinct from the
# docker compose CLI plugin). Provides `docker-compose` on PATH.
# Asset naming: docker-compose-linux-{ARCH} where ARCH is the raw uname -m
# suffix (x86_64 / aarch64) — confirmed against v5.1.3 release assets.
echo "Installing docker-compose v${DOCKER_COMPOSE_VERSION}..."
curl -fsSL \
  "https://github.com/docker/compose/releases/download/v${DOCKER_COMPOSE_VERSION}/docker-compose-linux-${ARCH}" \
  -o /usr/local/bin/docker-compose
chmod +x /usr/local/bin/docker-compose

# --- /usr/local/bin/docker shim ---
# Bash aliases don't expand in non-interactive shells (e.g. `agentbox exec . docker run ...`).
# A tiny exec shim makes `docker` work transparently in both interactive and scripted contexts.
# We do NOT use `set -e` inside the shim — it must pass through all exit codes from podman.
cat > /usr/local/bin/docker <<'EOF'
#!/usr/bin/env bash
exec podman "$@"
EOF
chmod 0755 /usr/local/bin/docker

# --- /etc/containers/storage.conf ---
# Configures rootless podman inside the agentbox box to use fuse-overlayfs as
# the overlay driver. This is what enables nested rootless containers without
# kernel overlay support (which is unavailable in an unprivileged container).
# Verified against `containers-storage` man page and podman documentation.
mkdir -p /etc/containers
cat > /etc/containers/storage.conf <<'STOCONF'
[storage]
driver = "overlay"
runroot = "/run/containers/storage"
graphroot = "/var/lib/containers/storage"

[storage.options]
mount_program = "/usr/bin/fuse-overlayfs"
STOCONF

# --- /etc/containers/registries.conf ---
# Standard unqualified-search-registries so bare image names (e.g. `hello-world`,
# `alpine`) resolve to docker.io. Overwriting instead of appending to avoid
# duplicates with any apt-installed defaults.
cat > /etc/containers/registries.conf <<'REGCONF'
unqualified-search-registries = ["docker.io", "quay.io", "ghcr.io"]
REGCONF

echo "containers kit install complete"
