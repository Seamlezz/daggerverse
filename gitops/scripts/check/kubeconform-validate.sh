#!/bin/sh
set -eu

SKIPS="${KUBECONFORM_SKIPS:-CustomResourceDefinition,VaultDynamicSecret}"
IGNORES="${KUBECONFORM_IGNORES:-gotk-components|gotk-sync}"

failed=0
for manifest in /tmp/manifests/*.yaml; do
  [ -f "$manifest" ] || continue
  echo "[kubeconform] $manifest"
  if ! kubeconform -summary \
    -skip "${SKIPS}" \
    -ignore-missing-schemas \
    -ignore-filename-pattern "${IGNORES}" \
    -schema-location default \
    -schema-location '/tmp/schemas/flux/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
    -schema-location '/tmp/schemas/helm/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
    -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
    "$manifest"; then
    failed=1
  fi
done

if [ "$failed" -ne 0 ]; then
  echo "[check] kubeconform failed" >&2
  exit 1
fi

echo "[check] kubeconform passed"
