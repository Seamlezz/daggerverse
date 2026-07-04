# Daggerverse — Seamlezz Dagger Module Catalog

Reusable Dagger modules for GitOps workflows, infrastructure validation, and more.

## Modules

- **[gitops](./gitops)** — Generic GitOps workflow module: Flux bootstrap, local Git mirror push/reload, and static validation checks (kustomize build, kubeconform, HelmRelease dry-run, SOPS decrypt, Terraform validate, YAML lint, Flux integrity).
- **[wash](./wash)** — wasmCloud `wash` toolchain module: build one or many components with `wash build`, auto-discover component directories from `.wash/config.yaml`, and publish OCI artifacts with version plus `latest` tags.
