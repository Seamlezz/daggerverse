#!/bin/sh
set -eu

env_dir="$1"

echo "[terraform] $env_dir"
mkdir -p /var/run/secrets/kubernetes.io/serviceaccount
printf '%s\n' 'eyJhbGciOiJSUzI1NiIsImtpZCI6ImRhZ2dlci1zdGF0aWMtY2hlY2sifQ.eyJhdWQiOlsidmF1bHQiXSwiaXNzIjoia3ViZXJuZXRlcy9zZXJ2aWNlYWNjb3VudCIsInN1YiI6InN5c3RlbTpzZXJ2aWNlYWNjb3VudDpvcGVuYmFvOnRmLXJ1bm5lciJ9.signature' > /var/run/secrets/kubernetes.io/serviceaccount/token
cd "/src/$env_dir"
terraform init -backend=false -input=false -upgrade >/dev/null
terraform validate
terraform fmt -check -recursive
