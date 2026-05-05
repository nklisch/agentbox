# containers kit env. Exports + PATH per KITS.md, plus aliases for interactive shells.

# Docker-compatible socket path (for the agentbox container running as root).
# The socket isn't auto-started in a non-systemd container; run:
#   podman system service --time=0 unix:///run/user/0/podman/podman.sock &
# if you need the Docker REST API.
export DOCKER_HOST="unix:///run/user/0/podman/podman.sock"

# Friendly aliases for interactive shells. Non-interactive scripts use the
# /usr/local/bin/docker shim from install.sh — bash aliases aren't expanded there.
# Note: aliases in env.sh are a KITS.md exception; documented here explicitly.
alias docker='podman'
alias docker-compose='podman-compose'
