# wash

Workspace-first Dagger module for building and publishing wasmCloud components.

The public API consists of `BuildComponents` and `PublishComponents`. The constructor requires a stable caller-owned `cacheNamespace`; `rootDir` and a full Rust image override are optional.

```bash
dagger -m ./wash call --cache-namespace=my-backend \
  build-components --component-dirs=components/a export --path=/tmp/components

dagger -m ./wash call --cache-namespace=my-backend \
  publish-components --registry=ghcr.io --repository=org/components --tag=1.2.3
```

An empty component list discovers `.wash/config.yaml` files. Compatible Preview 2 Cargo components (`cargo build --target wasm32-wasip2 --release`, `wit.skip_fetch: true`, no build environment, and workspace target artifacts) are grouped by workspace and Rust version and built with one Cargo command. Rust defaults to `rust:<workspace rust-version>-bookworm`. Other configurations retain `wash build` as a correctness fallback.

Cargo registry and git caches are stable per Rust version. Workspace target caches are locked and keyed by cache namespace, Rust version, target, and profile. Artifacts are copied out of mutable caches before export or publication.

Publication always pushes `latest` and additionally pushes a non-`latest` tag when supplied. Registry is a hostname with optional port and no scheme; `registryService` is an independent optional in-session service. Username and password must be supplied together. Ref pushes run concurrently with `maxParallel` (default 8), every ref is attempted, and failures are aggregated. A nonce invalidates only push executions, leaving toolchain and build nodes cacheable.

The module installs the wash CLI 2.5.2 Linux release binary selected for Dagger's default platform (`amd64` or `arm64`). Each asset is fetched directly with its pinned SHA-256 checksum and executable permissions; unsupported platforms are rejected.

The former `Container`, single-component build/publish, and WIT APIs were removed.
