#!/bin/sh
set -eu

ARCH="$(uname -m)"
case "$ARCH" in
  aarch64|arm64) KARCH=arm64 ;;
  *) KARCH=amd64 ;;
esac

curl -fsSL "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${KARCH}" \
  -o /usr/local/bin/yq
chmod +x /usr/local/bin/yq
