#!/bin/sh
set -eu

ARCH="$(uname -m)"
case "$ARCH" in
  aarch64|arm64) KARCH=arm64 ;;
  *) KARCH=amd64 ;;
esac

curl -fsSL "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize/${KUSTOMIZE_VERSION}/kustomize_${KUSTOMIZE_VERSION}_linux_${KARCH}.tar.gz" \
  | tar xz -C /usr/local/bin
