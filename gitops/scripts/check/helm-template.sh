#!/bin/sh
set -eu

MANIFEST_DIR=/tmp/manifests

KUBE_VERSION="${KUBE_VERSION:-1.33.0}"

. /usr/local/bin/check/helm-source-resolver.sh

template_release() {
  release_yaml="$1"
  name="$(printf '%s' "$release_yaml" | yq '.metadata.name' -o t)"
  namespace="$(printf '%s' "$release_yaml" | yq '.metadata.namespace' -o t)"
  chart_ref_kind="$(printf '%s' "$release_yaml" | yq '.spec.chartRef.kind // .spec.chart.spec.sourceRef.kind // ""' -o t)"
  chart_ref_name="$(printf '%s' "$release_yaml" | yq '.spec.chartRef.name // .spec.chart.spec.sourceRef.name // ""' -o t)"
  chart_name="$(printf '%s' "$release_yaml" | yq '.spec.chart.spec.chart // ""' -o t)"
  chart_version="$(printf '%s' "$release_yaml" | yq '.spec.chart.spec.version // ""' -o t)"
  values_file="$(mktemp)"

  printf '%s' "$release_yaml" | python3 /usr/local/bin/merge-helm-values.py >"$values_file"
  echo "[helm] $namespace/$name ($chart_ref_kind/$chart_ref_name)"
  case "$chart_ref_kind" in
    HelmRepository)
      repo_url="$(resolve_repo_url "$chart_ref_name")"
      helm template "$name" "$chart_name" --namespace "$namespace" --version "$chart_version" --repo "$repo_url" --kube-version "$KUBE_VERSION" -f "$values_file" >/dev/null
      ;;
    OCIRepository)
      oci_url="$(resolve_oci_url "$chart_ref_name")"
      oci_version="$(resolve_oci_version "$chart_ref_name")"
      helm template "$name" "$oci_url" --namespace "$namespace" --version "$oci_version" --kube-version "$KUBE_VERSION" -f "$values_file" >/dev/null
      ;;
    HelmChart)
      chart_path="$(resolve_helm_chart_path "$chart_ref_name")"
      helm template "$name" "/src/$chart_path" --namespace "$namespace" --kube-version "$KUBE_VERSION" -f "$values_file" >/dev/null
      ;;
    *)
      echo "unsupported chart source for $namespace/$name: $chart_ref_kind" >&2
      rm -f "$values_file"
      return 1
      ;;
  esac

  rm -f "$values_file"
}

for manifest in "$MANIFEST_DIR"/*.yaml; do
  [ -f "$manifest" ] || continue
  yq -o=json -I=0 'select(.kind == "HelmRelease")' "$manifest" | while IFS= read -r release_json; do
    [ -n "$release_json" ] || continue
    template_release "$(printf '%s' "$release_json" | yq -P '.')"
  done
done

echo "[check] helm template passed"
