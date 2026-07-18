# Daggerverse — Seamlezz Dagger Module Catalog

Reusable Dagger modules for GitOps workflows, infrastructure validation, and more.

## Modules

- **[gitops](./gitops)** — Generic GitOps workflow module: Flux bootstrap, local Git mirror push/reload, and static validation checks (kustomize build, kubeconform, HelmRelease dry-run, SOPS decrypt, Terraform validate, YAML lint, Flux integrity).
- **[wash](./wash)** — Workspace-first wasmCloud component builder and bounded OCI publisher, with shared Cargo builds, stable caches, and a correctness fallback to `wash build`.
