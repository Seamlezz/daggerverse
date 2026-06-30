#!/bin/sh
set -eu

MANIFEST_DIR=/tmp/manifests
OUT_DIR=/tmp/schemas/helm
mkdir -p "$OUT_DIR"

. /usr/local/bin/check/helm-source-resolver.sh

fetch_crds() {
  chart_ref_kind="$1"
  chart_ref_name="$2"
  chart_name="$3"
  chart_version="$4"
  tmp_chart="$(mktemp -d)"

  case "$chart_ref_kind" in
    HelmRepository)
      repo_url="$(resolve_repo_url "$chart_ref_name")"
      helm show crds "$chart_name" --version "$chart_version" --repo "$repo_url" >"$tmp_chart/crds.yaml"
      ;;
    OCIRepository)
      oci_url="$(resolve_oci_url "$chart_ref_name")"
      oci_version="$(resolve_oci_version "$chart_ref_name")"
      helm show crds "$oci_url" --version "$oci_version" >"$tmp_chart/crds.yaml"
      ;;
    HelmChart)
      chart_path="$(resolve_helm_chart_path "$chart_ref_name")"
      helm show crds "/src/$chart_path" >"$tmp_chart/crds.yaml"
      ;;
    *)
      rm -rf "$tmp_chart"
      return 0
      ;;
  esac

  if [ -s "$tmp_chart/crds.yaml" ]; then
    python3 /usr/local/bin/extract-crd-schemas.py "$OUT_DIR" "$tmp_chart/crds.yaml"
  fi
  rm -rf "$tmp_chart"
}

for manifest in "$MANIFEST_DIR"/*.yaml; do
  [ -f "$manifest" ] || continue
  while IFS="$(printf '\t')" read -r chart_ref_kind chart_ref_name chart_name chart_version; do
    [ -n "$chart_ref_kind" ] || continue
    fetch_crds "$chart_ref_kind" "$chart_ref_name" "$chart_name" "$chart_version"
  done <<EOF
$(yq -N '
  select(.kind == "HelmRelease") |
  [
    (.spec.chartRef.kind // .spec.chart.spec.sourceRef.kind // ""),
    (.spec.chartRef.name // .spec.chart.spec.sourceRef.name // ""),
    (.spec.chart.spec.chart // ""),
    (.spec.chart.spec.version // "")
  ] | @tsv
' "$manifest")
EOF
done
