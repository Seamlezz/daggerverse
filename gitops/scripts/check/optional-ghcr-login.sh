#!/bin/sh
set -eu

# Login to ghcr.io using a GitHub token from the GITHUB_TOKEN env var.
# If GITHUB_TOKEN is not set, skip silently.

if [ -z "${GITHUB_TOKEN:-}" ]; then
  exit 0
fi

echo "$GITHUB_TOKEN" | helm registry login ghcr.io --username "oauth2accesstoken" --password-stdin >/dev/null 2>&1 || true
