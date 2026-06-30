#!/bin/sh
set -eu

curl -fsSL https://raw.githubusercontent.com/external-secrets/external-secrets/main/deploy/crds/bundle.yaml \
  -o /tmp/eso-crds.yaml
python3 /usr/local/bin/extract-crd-schemas.py /tmp/schemas/helm /tmp/eso-crds.yaml
