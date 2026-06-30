#!/bin/sh
set -eu

count=0
while IFS= read -r file; do
  [ -n "$file" ] || continue
  echo "[sops] $file"
  sops -d "/src/$file" >/dev/null
  count=$((count + 1))
done < /tmp/enc-files.txt

echo "[check] sops decrypt passed ($count files)"
