# wash

Reusable Dagger module for wasmCloud `wash` workflows.

## What it provides

- A reusable Rust + `wash` toolchain container.
- `wash build` for one component.
- Multi-component builds from explicit paths or auto-discovery.
- OCI publishing with `wash oci push`.
- Hermetic WIT dependency fetching with locked, clean output.
- Optional registry credentials.
- Always publishes `latest`, and optionally an additional version tag.

Defaults:

- `wash` version: `2.5.1`
- Rust image: `rust:latest`
- Rust target: `wasm32-wasip2`

## Component expectations

Each component should contain `.wash/config.yaml` with `build.component_path`:

```yaml
build:
  command: cargo build --target wasm32-wasip2 --release
  component_path: target/wasm32-wasip2/release/my_component.wasm
```

`component_path` is resolved relative to the component directory after `wash build` runs.

When `.wash/config.yaml` contains `wit.skip_fetch: true`, the module runs `wash build --skip-fetch`; otherwise it runs plain `wash build`.

## Caching

The module uses Dagger cache volumes for Cargo downloads and component build output:

- `/usr/local/cargo/registry`
- `/usr/local/cargo/git`
- the nearest `target` directory above `build.component_path`, resolved from `.wash/config.yaml`

For a component-local artifact path such as:

```yaml
build:
  component_path: target/wasm32-wasip2/release/my_component.wasm
```

this mounts `/workspace/<component>/target`.

For a workspace-level artifact path such as:

```yaml
build:
  component_path: ../../target/wasm32-wasip2/release/auth_callout.wasm
```

this mounts `/workspace/target`.

The final wasm artifact is copied to `/wash-artifacts` before being returned or pushed, so `target` can remain a cache mount. Cache volume keys are still per component.

The default Rust image remains `rust:latest`; callers can still pass `--rust-image` if they want stricter reproducibility.

## Build one component

Run Dagger from the workspace/repository root, or use `--root-dir` to select a subdirectory of the current workspace.

```bash
cd /path/to/repo
dagger -m /path/to/daggerverse/wash call \
  build --component-dir=components/nats-echo \
  export --path=/tmp/nats_echo.wasm
```

For a workspace where component artifact paths refer to a shared root, run from a parent workspace and set `rootDir` to that root:

```bash
cd /Users/thijs/Bestanden/TypeWriter/features/v1
dagger -m /path/to/daggerverse/wash call \
  --root-dir=backend \
  build --component-dir=access/auth-callout \
  export --path=/tmp/auth_callout.wasm
```

## Build multiple components

Explicit paths:

```bash
cd /path/to/repo
dagger -m /path/to/daggerverse/wash call \
  build-components \
  --component-dirs=components/nats-echo \
  --component-dirs=components/smoke-counter \
  export --path=/tmp/components
```

Auto-discover components by leaving `componentDirs` empty. Discovery finds `**/.wash/config.yaml`:

```bash
cd /path/to/repo
dagger -m /path/to/daggerverse/wash call \
  build-components \
  export --path=/tmp/components
```

The output directory contains one artifact per component, named from the component directory basename:

```text
nats-echo.wasm
smoke-counter.wasm
```

## Fetch WIT dependencies

`WitFetch` accepts a source directory and explicit component directories, sorts and deduplicates the paths, and runs `wash wit fetch --clean --non-interactive` in each component with the pinned toolchain container. It returns the updated source directory. `WitFetchChanges` returns the same result as a changeset.

```bash
cd /path/to/repo
dagger -m /path/to/daggerverse/wash call \
  wit-fetch --source=. \
  --component-dirs=backend/access/auth-callout \
  --component-dirs=backend/wasmcloud-utils \
  export --path=/tmp/wit-source
```

The source directory must include the repository-root `wkg.lock` and each component's WIT sources. That root file is the sole dependency lock: each fetch temporarily exposes it at the component lock path through a symlink, then removes the symlink from the output. Repositories migrating to this API should remove component-local lock files from version control.

## Publish one component

`Publish` runs `wash build` and then pushes `latest` plus the optional tag.

```bash
cd /path/to/repo
dagger -m /path/to/daggerverse/wash call \
  publish \
  --component-dir=components/nats-echo \
  --registry=ghcr.io \
  --repository=seamlezz/wasmcloud-smoke \
  --component-name=nats-echo \
  --tag=0.1.0
```

Pushes:

```text
ghcr.io/seamlezz/wasmcloud-smoke/nats-echo:0.1.0
ghcr.io/seamlezz/wasmcloud-smoke/nats-echo:latest
```

If `componentName` is omitted, the component directory basename is used.

## Publish multiple components

Explicit paths:

```bash
cd /path/to/repo
dagger -m /path/to/daggerverse/wash call \
  publish-components \
  --component-dirs=components/nats-echo \
  --component-dirs=components/smoke-counter \
  --registry=ghcr.io \
  --repository=seamlezz/wasmcloud-smoke \
  --tag=0.1.0
```

Auto-discovery:

```bash
cd /path/to/repo
dagger -m /path/to/daggerverse/wash call \
  publish-components \
  --registry=ghcr.io \
  --repository=seamlezz/wasmcloud-smoke \
  --tag=0.1.0
```

For multi-component publish, image names come from component directory basenames.

## Authenticated publish

Use a Dagger secret for the registry password or token:

```bash
cd /path/to/repo
dagger -m /path/to/daggerverse/wash call \
  publish-components \
  --registry=ghcr.io \
  --repository=seamlezz/wasmcloud-smoke \
  --tag=0.1.0 \
  --username="$GHCR_USER" \
  --password=env:GHCR_TOKEN
```

## Insecure local registry

```bash
cd /path/to/repo
dagger -m /path/to/daggerverse/wash call \
  publish \
  --component-dir=components/nats-echo \
  --registry=localhost:5000 \
  --repository=wasmcloud \
  --tag=dev \
  --insecure=true
```

## Reuse from other Dagger modules

Other modules can call `Container()` to get a container with Rust, `wasm32-wasip2`, and `wash` installed, then add their own source mounts or commands.

```go
c := dag.Wash(source).Container()
```

Use `Build`, `BuildComponents`, `WitFetch`, `WitFetchChanges`, `Publish`, and `PublishComponents` for the standard component workflows.
