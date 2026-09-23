#!/usr/bin/env bash
# Installs the development toolchain into user-owned directories (no sudo).
#   Go            -> ~/.local/go
#   kubebuilder, kind, kubectl, helm -> ~/.local/bin
# Re-runnable; versions are pinned below and can be overridden via env vars.
set -euo pipefail

GO_VERSION="${GO_VERSION:-1.27.1}"
KUBEBUILDER_VERSION="${KUBEBUILDER_VERSION:-4.16.0}"
KIND_VERSION="${KIND_VERSION:-0.33.0}"
HELM_VERSION="${HELM_VERSION:-4.3.0}"
KUBECTL_VERSION="${KUBECTL_VERSION:-$(curl -fsSL https://dl.k8s.io/release/stable.txt)}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
GO_ROOT="${GO_ROOT:-$HOME/.local/go}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$BIN_DIR"

log() { printf '==> %s\n' "$*"; }

# --- Go ---------------------------------------------------------------------
if [ -x "$GO_ROOT/bin/go" ] && "$GO_ROOT/bin/go" version | grep -q "go$GO_VERSION "; then
  log "Go $GO_VERSION already installed"
else
  log "Installing Go $GO_VERSION to $GO_ROOT"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.${OS}-${ARCH}.tar.gz" -o "$TMP/go.tgz"
  rm -rf "$GO_ROOT"
  mkdir -p "$(dirname "$GO_ROOT")"
  tar -C "$TMP" -xzf "$TMP/go.tgz"
  mv "$TMP/go" "$GO_ROOT"
fi

# --- kubebuilder ------------------------------------------------------------
log "Installing kubebuilder v$KUBEBUILDER_VERSION"
curl -fsSL "https://github.com/kubernetes-sigs/kubebuilder/releases/download/v${KUBEBUILDER_VERSION}/kubebuilder_${OS}_${ARCH}" -o "$TMP/kubebuilder"
install -m 0755 "$TMP/kubebuilder" "$BIN_DIR/kubebuilder"

# --- kind -------------------------------------------------------------------
log "Installing kind v$KIND_VERSION"
curl -fsSL "https://kind.sigs.k8s.io/dl/v${KIND_VERSION}/kind-${OS}-${ARCH}" -o "$TMP/kind"
install -m 0755 "$TMP/kind" "$BIN_DIR/kind"

# --- kubectl ----------------------------------------------------------------
log "Installing kubectl $KUBECTL_VERSION"
curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/${OS}/${ARCH}/kubectl" -o "$TMP/kubectl"
install -m 0755 "$TMP/kubectl" "$BIN_DIR/kubectl"

# --- helm -------------------------------------------------------------------
log "Installing helm v$HELM_VERSION"
curl -fsSL "https://get.helm.sh/helm-v${HELM_VERSION}-${OS}-${ARCH}.tar.gz" | tar -xz -C "$TMP"
install -m 0755 "$TMP/${OS}-${ARCH}/helm" "$BIN_DIR/helm"

cat <<MSG

Done. Make sure the following is in your shell profile (~/.zshrc or ~/.bashrc):

  export PATH="$GO_ROOT/bin:\$HOME/go/bin:$BIN_DIR:\$PATH"

Installed:
  $("$GO_ROOT/bin/go" version)
  kubebuilder $("$BIN_DIR/kubebuilder" version 2>/dev/null | head -1)
  $("$BIN_DIR/kind" version)
  kubectl $("$BIN_DIR/kubectl" version --client 2>/dev/null | head -1)
  $("$BIN_DIR/helm" version --short)
MSG
