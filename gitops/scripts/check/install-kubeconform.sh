#!/bin/sh
set -eu

ARCH="$(uname -m)"
case "$ARCH" in
  aarch64|arm64) KARCH=arm64 ;;
  *) KARCH=amd64 ;;
esac

curl -fsSL "https://github.com/yannh/kubeconform/releases/download/${KUBECONFORM_VERSION}/kubeconform-linux-${KARCH}.tar.gz" \
  | tar xz -C /usr/local/bin
