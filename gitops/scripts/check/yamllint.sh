#!/bin/sh
set -eu

while IFS= read -r file; do
  [ -n "$file" ] || continue
  yamllint -c /tmp/.yamllint "/src/$file"
done < /tmp/yaml-files.txt

echo "[check] yamllint passed"
