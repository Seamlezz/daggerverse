#!/bin/sh
# Resolves HelmRepository URLs, OCI repository URLs, and HelmChart paths.
# Source directories are provided via env vars (colon-separated):
#   HELM_SOURCE_DIRS  - directories containing HelmRepository YAML files
#   OCI_SOURCE_DIRS   - directories containing OCIRepository YAML files
#   GIT_SOURCE_DIRS   - directories containing HelmChart YAML files

resolve_repo_url() {
  for dir in $(printf '%s' "${HELM_SOURCE_DIRS:-infrastructure/sources/helm}" | tr ':' ' '); do
    url=$(yq -N 'select(.kind == "HelmRepository" and .metadata.name == "'"$1"'") | .spec.url' \
      "/src/${dir}"/*.yaml -o t 2>/dev/null | head -n1)
    if [ -n "$url" ] && [ "$url" != "null" ]; then
      printf '%s' "$url"
      return
    fi
  done
}

resolve_oci_url() {
  for dir in $(printf '%s' "${OCI_SOURCE_DIRS:-infrastructure/sources/oci}" | tr ':' ' '); do
    url=$(yq -N 'select(.kind == "OCIRepository" and .metadata.name == "'"$1"'") | .spec.url' \
      "/src/${dir}"/*.yaml -o t 2>/dev/null | head -n1)
    if [ -n "$url" ] && [ "$url" != "null" ]; then
      printf '%s' "$url"
      return
    fi
  done
}

resolve_oci_version() {
  for dir in $(printf '%s' "${OCI_SOURCE_DIRS:-infrastructure/sources/oci}" | tr ':' ' '); do
    tag=$(yq -N 'select(.kind == "OCIRepository" and .metadata.name == "'"$1"'") | .spec.ref.tag' \
      "/src/${dir}"/*.yaml -o t 2>/dev/null | head -n1)
    if [ -n "$tag" ] && [ "$tag" != "null" ]; then
      printf '%s' "$tag"
      return
    fi

    semver=$(yq -N 'select(.kind == "OCIRepository" and .metadata.name == "'"$1"'") | .spec.ref.semver' \
      "/src/${dir}"/*.yaml -o t 2>/dev/null | head -n1)
    if [ -n "$semver" ] && [ "$semver" != "null" ]; then
      printf '%s' "$semver"
      return
    fi
  done
}

resolve_helm_chart_path() {
  for dir in $(printf '%s' "${GIT_SOURCE_DIRS:-infrastructure/sources/git}" | tr ':' ' '); do
    path=$(yq -N 'select(.kind == "HelmChart" and .metadata.name == "'"$1"'") | .spec.chart' \
      "/src/${dir}"/*.yaml -o t 2>/dev/null | head -n1)
    if [ -n "$path" ] && [ "$path" != "null" ]; then
      printf '%s' "$path"
      return
    fi
  done
}
