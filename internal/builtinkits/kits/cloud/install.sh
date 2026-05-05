#!/usr/bin/env bash
# cloud kit installer: aws-cli v2, gcloud, az, terraform, kubectl, helm.
# All tool versions are pinned at the top for reproducible rebuilds.
# gcloud and az track their respective apt repos ("latest" on each repo update).
# Verified against upstream release APIs on 2026-05-05.
set -euo pipefail

# --- Pinned versions (env-var overridable) ---
AWSCLI_VERSION="${AWSCLI_VERSION:-2.34.42}"      # aws/aws-cli tag — GitHub confirms; awscli.amazonaws.com publishes versioned zips
TERRAFORM_VERSION="${TERRAFORM_VERSION:-1.15.1}" # HashiCorp releases API: api.releases.hashicorp.com/v1/releases/terraform/latest
KUBECTL_VERSION="${KUBECTL_VERSION:-1.36.0}"     # dl.k8s.io/release/stable.txt — v-prefix stripped
HELM_VERSION="${HELM_VERSION:-4.1.4}"            # helm/helm — v-prefix stripped from tag v4.1.4
# gcloud and az: installed from their apt repos — version tracks the repo's current release.

# --- Arch detection ---
# ARCH is raw uname -m output (x86_64 / aarch64). aws-cli and gcloud use this directly.
# GO_ARCH (amd64 / arm64) is used for kubectl, terraform, and helm.
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)
    RUST_TRIPLE="x86_64-unknown-linux-gnu"
    GO_ARCH="amd64"
    ;;
  aarch64)
    RUST_TRIPLE="aarch64-unknown-linux-gnu"
    GO_ARCH="arm64"
    ;;
  *)
    echo "unsupported arch: $ARCH" >&2
    exit 1
    ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- Wire env.d into zsh (idempotent; first kit to run does it once) ---
if ! grep -q "agentbox/env.d" /etc/zsh/zshenv 2>/dev/null; then
  cat >> /etc/zsh/zshenv <<'ZSHENV'

# --- agentbox kit env ---
for _f in /etc/agentbox/env.d/*.sh; do
  [ -r "$_f" ] && . "$_f"
done
unset _f
ZSHENV
fi

# --- aws-cli v2 ---
# Versioned zip from awscli.amazonaws.com. The ARCH in the filename is the raw
# uname -m output (x86_64 or aarch64), NOT the Go arch convention.
# The bundled installer places the CLI at /usr/local/aws-cli and symlinks to /usr/local/bin.
echo "Installing aws-cli v${AWSCLI_VERSION}..."
curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${ARCH}-${AWSCLI_VERSION}.zip" \
  -o "$TMP/awscli.zip"
unzip -q "$TMP/awscli.zip" -d "$TMP/awscli-extract"
"$TMP/awscli-extract/aws/install" \
  --bin-dir /usr/local/bin \
  --install-dir /usr/local/aws-cli \
  --update

# --- gcloud (Google Cloud SDK) ---
# Add Google's apt repo (packages.cloud.google.com/apt). The key is fetched and stored
# in /usr/share/keyrings/ using the modern gpg dearmor method.
# base kit supplies ca-certificates, curl, and gnupg already.
echo "Installing gcloud via Google apt repo..."
curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg \
  | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg
echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" \
  > /etc/apt/sources.list.d/google-cloud-sdk.list
apt-get update -qq
apt-get install -y --no-install-recommends google-cloud-cli
rm -rf /var/lib/apt/lists/*

# --- az (Azure CLI) ---
# Microsoft's officially supported install for Debian-based systems.
# The Microsoft signing key is fetched and stored in /etc/apt/keyrings/.
# Repo URL targets Debian Bookworm (the base kit's OS).
echo "Installing az via Microsoft apt repo..."
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://packages.microsoft.com/keys/microsoft.asc \
  | gpg --dearmor -o /etc/apt/keyrings/microsoft.gpg
echo "deb [arch=${GO_ARCH} signed-by=/etc/apt/keyrings/microsoft.gpg] https://packages.microsoft.com/repos/azure-cli/ bookworm main" \
  > /etc/apt/sources.list.d/azure-cli.list
apt-get update -qq
apt-get install -y --no-install-recommends azure-cli
rm -rf /var/lib/apt/lists/*

# --- terraform ---
# HashiCorp publishes versioned zips at releases.hashicorp.com.
# Archive name: terraform_{VERSION}_linux_{GO_ARCH}.zip; contains a single binary.
echo "Installing terraform v${TERRAFORM_VERSION}..."
curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${GO_ARCH}.zip" \
  -o "$TMP/terraform.zip"
unzip -q "$TMP/terraform.zip" -d "$TMP/terraform-extract"
install -m 0755 "$TMP/terraform-extract/terraform" /usr/local/bin/terraform

# --- kubectl ---
# Single binary published at dl.k8s.io. No archive to extract.
# The stable.txt file gives the latest stable minor; we pin for reproducibility.
echo "Installing kubectl v${KUBECTL_VERSION}..."
curl -fsSL "https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/linux/${GO_ARCH}/kubectl" \
  -o /usr/local/bin/kubectl
chmod +x /usr/local/bin/kubectl

# --- helm (Kubernetes package manager) ---
# Official install script from helm/helm. DESIRED_VERSION is passed as an env var;
# the script downloads and installs the binary to /usr/local/bin/helm.
echo "Installing helm v${HELM_VERSION}..."
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 \
  | DESIRED_VERSION="v${HELM_VERSION}" bash

echo "cloud kit install complete"
