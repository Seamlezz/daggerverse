#!/bin/sh
set -eu

env_dir="$1"

echo "[terraform] $env_dir"
cd "/src/$env_dir"
terraform init -backend=false -input=false -upgrade >/dev/null
terraform validate
terraform fmt -check -recursive
