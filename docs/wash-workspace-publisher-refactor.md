# Workspace-first `wash` publisher rewrite

## Context

The current `wash` module grew incrementally around one `wash build` container per component. It now works functionally, but its execution and cache model are a poor fit for Typewriter:

- Typewriter has eight publishable wasmCloud components in one Cargo workspace under `backend/`.
- Every component writes to the same workspace target directory (`backend/target`), but `wash/main.go` assigns a different target cache volume per component. Shared dependencies are therefore compiled into eight isolated caches.
- `PublishComponents` creates one branch per ref and forces the branches through a synthetic `Directory.Sync`. Dagger documents `Sync` as evaluating the reachable DAG, but does not guarantee scheduling width for a composite directory. The current code therefore does not provide an explicit, testable component-publication concurrency contract.
- `rust:latest` and the mutable `https://wasmcloud.com/sh` installer weaken reproducibility and can invalidate otherwise stable toolchain layers.
- `wash oci push` is an ordinary cacheable `WithExec`. A repeated publish can be skipped by Dagger unless only the side-effecting push node is deliberately invalidated.
- Registry address and service binding are conflated, explicit component paths are insufficiently validated, build artifact names can collide, and tests do not exercise build or publish behavior.

The earlier plans in `docs/parallel-publish-components.md`, `docs/wash-cache-volumes.md`, and `docs/wash-target-cache-mount.md` optimized the component-at-a-time design. This plan supersedes that direction: compile a compatible Cargo workspace once, then fan out explicit bounded OCI pushes.

Evidence and guidance used for this design:

- Dagger's official concurrent-function recipe uses native Go concurrency around terminal evaluations: <https://docs.dagger.io/cookbook/builds/#execute-functions-concurrently>.
- `Sync` force-evaluates an object's dependency DAG, but the API does not promise that a composite `Directory.Sync` schedules every leaf concurrently: <https://pkg.go.dev/dagger.io/dagger@v0.21.7#Syncer>.
- Dagger cache volumes support `SHARED`, `PRIVATE`, and `LOCKED`; `LOCKED` shares state while serializing writers: <https://docs.dagger.io/api/reference/#definition-CacheSharingMode>.
- Cache-volume contents do not invalidate Dagger layer cache, so mutable caches must accelerate a correctly ordered DAG rather than act as output transport.
- Cargo workspace package selection supports one invocation with repeated `--package` arguments: <https://doc.rust-lang.org/cargo/commands/cargo-build.html>.
- wasmCloud `wash build` primarily fetches WIT when enabled, runs the configured command, validates the configured artifact, and may wrap Preview 1 core modules. The optimized direct-Cargo path must therefore be limited to known-compatible Preview 2 Cargo configurations; all other configurations use `wash build` as a fallback.

## Goals and non-goals

### Goals

1. Make `PublishComponents` the primary workflow and make its concurrency explicit and testable.
2. Compile all compatible requested packages in the same Cargo workspace with one Cargo invocation.
3. Reuse one stable workspace target cache across source edits and across all components.
4. Ensure an unchanged build is a Dagger layer-cache hit and a changed build reuses Cargo outputs instead of recompiling dependencies from scratch.
5. Publish every component's version and `latest` refs concurrently with a configurable bound, defaulting to 8.
6. Attempt every ref even when some pushes fail, then report all per-ref failures together.
7. Keep `BuildComponents` for artifact export and debugging.
8. Preserve `.wash/config.yaml` discovery and provide a safe independent-build fallback for configurations that cannot use the Cargo workspace fast path.
9. Derive the Rust image version from Cargo workspace metadata, while keeping an explicit image override for exceptional cases.
10. Pin `wash` in the module release and verify downloaded tool artifacts.
11. Validate paths, artifact names, image names, registry inputs, credentials, and collisions before expensive execution.
12. Add public-boundary integration tests and focused planner/concurrency unit tests.
13. Release the breaking module as `wash/v0.7.0` and migrate Typewriter to it.

### Non-goals

1. Do not retain `WitFetch` or `WitFetchChanges` in `wash`.
2. Do not preserve the old public API. `Container`, single-component `Build`, and single-component `Publish` are removed.
3. Do not move the removed WIT workflow elsewhere. Typewriter's `WitSync`/`WitCheck` workflow is removed.
4. Do not implement changed-components-only Git selection.
5. Do not promise workspace batching for arbitrary shell commands, Preview 1 modules, non-Cargo projects, custom build environments, or configurations that fetch WIT during build. Those use the correctness-first fallback.
6. Do not make OCI publication atomic; registries cannot roll back refs already pushed when another ref fails.
7. Do not commit or push Typewriter changes as part of the release unless separately requested. The selected rollout updates its working tree and pin only.

## Current behavior

### Build and cache behavior

`wash/main.go` currently:

1. Loads `.wash/config.yaml` repeatedly for artifact path, build arguments, and cache mount selection.
2. Creates one toolchain/build container per component.
3. Mounts the workspace source into every build.
4. Mounts a target cache at the configured workspace target path but keys it by component directory (`targetCacheKey(componentDir)`).
5. Runs `wash build` once per component.
6. Copies each final artifact out of the target cache.

For Typewriter, all eight `.wash/config.yaml` files point through `../../target/...`, so all builds use `/workspace/target` but receive different cache contents. Common dependencies are compiled repeatedly.

### Publish behavior

`PublishComponents` builds ref containers in a Go loop and `syncPublishContainers` attaches marker files from those containers to one synthetic directory. A single `Directory.Sync` forces the result. This permits engine parallelism but does not explicitly submit bounded concurrent terminal calls, does not aggregate every failure, and has no concurrency test.

The push `WithExec` has no cache invalidation boundary, so Dagger can reuse a prior push execution even though registry publication is a side effect that must run for every invocation.

### Public API and downstream use

The module exposes `Container`, single/multi build, single/multi publish, and WIT-fetch functions. Typewriter installs `wash/v0.6.3` as a toolchain, configures the old publish arguments in `.env.example`, and calls `dag.Wash().WitFetchChanges(...)` from `.dagger/wit.go`.

## Proposed behavior

### Public API

The rewritten module exposes only multi-component build and publish:

```go
type Wash struct {
    Source         *dagger.Directory
    RootDir        string
    CacheNamespace string
    RustImage      string // optional override; empty means derive from Cargo
}

func New(
    source *dagger.Workspace,
    // Stable caller-owned identity, for example "typewriter-backend".
    cacheNamespace string,
    // +optional
    // +default="/"
    rootDir string,
    // Optional full Rust image override. By default use
    // rust:<workspace rust-version>-bookworm.
    // +optional
    rustImage string,
) *Wash

func (m *Wash) BuildComponents(
    ctx context.Context,
    // Empty means discover **/.wash/config.yaml.
    // +optional
    componentDirs []string,
) (*dagger.Directory, error)

func (m *Wash) PublishComponents(
    ctx context.Context,
    // OCI reference host, without a scheme, for example ghcr.io or registry:5000.
    registry string,
    // +optional
    repository string,
    // Empty means discover **/.wash/config.yaml.
    // +optional
    componentDirs []string,
    // Optional in-session service backing registry. External registries omit it.
    // +optional
    registryService *dagger.Service,
    // Optional version tag; latest is always also published.
    // +optional
    tag string,
    // +optional
    username string,
    // +optional
    password *dagger.Secret,
    // +optional
    // +default=false
    insecure bool,
    // Maximum simultaneous ref pushes.
    // +optional
    // +default=8
    maxParallel int,
) (string, error)
```

On success, `PublishComponents` returns deterministic newline-separated refs. On failure it waits for every scheduled ref and returns one deterministic aggregate error containing each failed ref and its error plus the refs that succeeded. The method still returns an error so CI fails correctly.

### Component identity and tags

- Components remain discoverable through `.wash/config.yaml` or an explicit directory list.
- A Cargo package name is the component ID, exported artifact basename, and OCI image name for workspace builds.
- The fallback uses the component directory basename.
- Duplicate component IDs, artifact names, or final OCI image bases are rejected before build.
- Every component publishes `:<tag>` when a non-empty non-`latest` tag is supplied, followed by `:latest` in deterministic reporting order.

### Workspace-first build planner

Read every selected config once and produce a pure plan before creating expensive execution nodes:

```go
type componentPlan struct {
    Dir             string
    ID              string
    ConfigPath      string
    Command         string
    ComponentPath   string
    ArtifactPath    string
    WorkspaceRoot   string
    ManifestPath    string
    PackageName     string
    RustVersion     string
    Target          string
    Profile         string
    SkipWitFetch    bool
    FastPathEligible bool
}

type buildGroup struct {
    WorkspaceRoot string
    RustVersion   string
    RustImage     string
    Target        string
    Profile       string
    Components    []componentPlan
    FastPath      bool
}
```

The planner will:

1. Normalize, sort, deduplicate, and boundary-check component directories.
2. Parse `.wash/config.yaml`, including `build.command`, `build.env`, `build.component_path`, and `wit.skip_fetch`.
3. Locate the nearest Cargo workspace manifest without allowing paths to escape the selected source root.
4. Parse Cargo manifests/metadata to map component directories to package names and read `workspace.package.rust-version` (or a package-level version when not inherited).
5. Resolve and validate artifact paths below the source root.
6. Group compatible components by workspace, Rust version, target, and profile.
7. Reject all naming/path collisions before execution.

A group is eligible for the direct Cargo fast path only when all selected components are standard Cargo Preview 2 builds equivalent to:

```text
cargo build --target wasm32-wasip2 --release
```

and:

- `wit.skip_fetch` is true;
- `build.env` is empty;
- each component maps to a Cargo package in the same workspace;
- each configured artifact resolves under that workspace's `target/wasm32-wasip2/release` directory.

Any incompatible component is routed to an independent `wash build` fallback. The fallback preserves wash's WIT-fetch, environment, custom command, validation, and Preview 1 wrapping semantics. Mixed calls may contain one or more workspace groups plus fallback components.

### One Cargo invocation per compatible workspace

For Typewriter, the fast-path command is structurally:

```bash
cargo build \
  --locked \
  --target wasm32-wasip2 \
  --release \
  --package auth-callout \
  --package auth-sentinel \
  --package auth-typewriter-permissions \
  --package organization-members \
  --package organization-roles \
  --package user-organization \
  --package service-identity \
  --package service-registration
```

Cargo performs its own parallel compilation and compiles shared dependencies once. After the build, one copy command moves every configured `.wasm` artifact from the target cache to stable `/out/<component-id>.wasm` paths. Returned/published artifacts always come from `/out`, never directly from a mutable cache mount.

### Cache architecture and DAG order

Use schema-versioned cache keys so future incompatible cache layouts can be intentionally rotated:

```text
wash-v2/cargo-registry/<rust-version>
wash-v2/cargo-git/<rust-version>
wash-v2/target/<cache-namespace>/<rust-version>/<target>/<profile>
```

- Cargo registry/git caches use `SHARED`; Cargo is responsible for its own download-cache locking.
- A workspace target cache uses `LOCKED`, protecting concurrent Dagger invocations from writing the same Cargo target simultaneously.
- There is only one Cargo process per build group, so `LOCKED` does not serialize components within a publish invocation.
- The required caller-owned `cacheNamespace` keeps unrelated repositories from sharing a target cache while remaining stable across source edits.

Create a manifest-only source snapshot (`Cargo.toml`, `Cargo.lock`, `.cargo/**`, and member manifests) and run `cargo fetch --locked --target <target>` before adding full source. Derive the build container from that fetch node so execution order is explicit. Adding full source afterward means ordinary source edits invalidate the build node but not the dependency-fetch node. The persistent target cache then lets Cargo revalidate and rebuild only affected packages.

The source snapshot continues to exclude `.git`, `target`, local credentials, and unrelated generated output. It must not over-filter Cargo workspace members, path dependencies, build scripts, WIT inputs, or other files consumed by custom commands.

### Reproducible toolchain

- Fix wash at 2.5.2 for this module release; remove the public wash-version override.
- Download the official platform-specific wash release artifact using a versioned URL and a committed SHA-256 checksum for each supported Dagger platform (`linux/amd64`, `linux/arm64`). Do not execute the mutable installer URL.
- Derive the normal Rust image as `rust:<rust-version>-bookworm` from Cargo metadata. An explicit full image override remains available for unusual registries/platforms.
- Install only the derived target(s) needed by the build group.
- Keep stable toolchain setup before source mounts so source edits cannot invalidate it.

### Explicit bounded ref publication

Build artifacts first as a shared lazy DAG. For each final ref, create a small clean push container that contains the pinned wash binary and exactly one artifact. Bind a registry service only when one was supplied; derive its DNS alias from the validated registry host while retaining any port in the OCI ref.

Force only the side-effecting push node to execute on every invocation:

```go
push := toolchain.
    WithFile("/artifact/component.wasm", artifact).
    WithServiceBinding(...). // only when registryService != nil
    WithEnvVariable("_WASH_PUBLISH_NONCE", invocationNonce).
    WithExec(pushArgs)
```

`invocationNonce` is added after all cacheable toolchain/build/artifact nodes, so it never busts compile caches.

Submit terminal calls explicitly with a bounded group. Jobs write to unique indexed result slots and return `nil` to the group so one failed ref does not cancel the rest:

```go
g := new(errgroup.Group)
g.SetLimit(maxParallel)
results := make([]publishResult, len(jobs))
for i := range jobs {
    i := i
    g.Go(func() error {
        _, err := jobs[i].Container.Sync(ctx)
        results[i] = publishResult{Ref: jobs[i].Ref, Err: err}
        return nil // collect after every job has run
    })
}
_ = g.Wait()
return formatPublishResults(results)
```

External context cancellation still stops work. Validation rejects `maxParallel < 1`.

### Typewriter migration

After local verification and the `wash/v0.7.0` release:

1. Remove `.dagger/wit.go`, including `WitSync`, `WitCheck`, and the `dag.Wash().WitFetchChanges` dependency.
2. Update `dagger.json` to `wash@wash/v0.7.0` and its exact release commit pin.
3. Configure the required constructor cache namespace as `typewriter-backend`.
4. Update Typewriter's root `.env.example` explicitly for the new constructor and publish arguments. Replace the current block:

   ```dotenv
   WASH_ROOTDIR="/backend/"
   WASH_PUBLISHCOMPONENTS_REGISTRY=tcp://oci.local.seamlezz.net:443
   WASH_PUBLISHCOMPONENTS_REGISTRYHOSTNAME=oci.local.seamlezz.net
   WASH_PUBLISHCOMPONENTS_REPOSITORY="typewriter"
   WASH_PUBLISHCOMPONENTS_USERNAME="gabber235"
   WASH_PUBLISHCOMPONENTS_PASSWORD=file://./backend/.artifact_keeper_password
   ```

   with the new shape:

   ```dotenv
   WASH_ROOTDIR="/backend/"
   WASH_CACHENAMESPACE="typewriter-backend"
   WASH_PUBLISHCOMPONENTS_REGISTRY="oci.local.seamlezz.net"
   WASH_PUBLISHCOMPONENTS_REGISTRYSERVICE=tcp://oci.local.seamlezz.net:443
   WASH_PUBLISHCOMPONENTS_REPOSITORY="typewriter"
   WASH_PUBLISHCOMPONENTS_USERNAME="gabber235"
   WASH_PUBLISHCOMPONENTS_PASSWORD=file://./backend/.artifact_keeper_password
   ```

   `WASH_PUBLISHCOMPONENTS_REGISTRYHOSTNAME` is removed. `REGISTRY` now always means the OCI reference host; `REGISTRYSERVICE` is the optional Dagger service transport. Confirm the exact generated environment-variable spelling after `dagger develop` and keep `.env.example` synchronized with the generated schema.
5. Regenerate Typewriter's Dagger SDK and remove stale generated WIT method bindings.
6. Verify the Typewriter module and its remaining generated checks/functions.

## Implementation approach

### 1. Replace the module around a pure planner

Rewrite `wash/main.go` rather than incrementally adapting `componentContainer`, `buildContainer`, and `syncPublishContainers`. Keep planning helpers free of Dagger terminal calls where possible, and isolate the necessary `Contents`/`Glob` metadata reads in one planning phase.

Suggested internal boundaries:

```go
func (m *Wash) resolveComponents(ctx context.Context, dirs []string) ([]componentPlan, error)
func groupBuilds(components []componentPlan, rustOverride string) ([]buildGroup, error)
func (m *Wash) buildGroup(group buildGroup) (*dagger.Container, map[string]*dagger.File, error)
func (m *Wash) buildFallback(component componentPlan) (*dagger.File, error)
func (m *Wash) artifacts(ctx context.Context, dirs []string) (*artifactSet, error)
func validatePublishInput(...) error
func refsFor(componentID, registry, repository, tag string) []string
func runPublishJobs(ctx context.Context, jobs []publishJob, limit int) []publishResult
```

Use `path` for container/source-relative paths, `filepath` only for local implementation paths if unavoidable, and one canonical containment helper for all user/config paths.

### 2. Build a clean toolchain factory

Create an internal toolchain function parameterized by the planned Rust version/image and target. Its graph contains the pinned base image, required system libraries, target installation, and checksum-verified wash binary. It does not mount source or mutable target output.

### 3. Implement fast-path and fallback builders

- Fast path: manifest-only fetch -> full source -> one selected-package Cargo build -> copy all artifacts to `/out`.
- Fallback: one `wash build` per incompatible component, with a safe cache identity and artifact copy-out. Independent fallback DAGs may execute in parallel when their cache mounts do not conflict; shared target caches use `LOCKED` for correctness.
- `BuildComponents` assembles deterministic artifact files and rejects collisions rather than silently overwriting.

### 4. Implement publication as a separate side-effect boundary

Publication consumes built `dagger.File` objects. It never rebuilds a component independently per tag. Version and latest refs branch from the same artifact, apply a nonce only at the push exec, execute through explicit bounded terminal calls, and aggregate ordered results.

### 5. Remove unrelated WIT code and dependencies

Delete WIT lock models, merge helpers, WIT functions, and `test-wit-fetch.sh`. Remove now-unused TOML/WIT dependencies only if Cargo manifest parsing does not require them; retain a TOML parser for Cargo planning as needed.

### 6. Add unit and Dagger integration tests

Unit tests cover:

- component path normalization, traversal rejection, and source-root containment;
- `.wash/config.yaml` parsing and fast-path eligibility;
- Cargo workspace/package/rust-version planning;
- deterministic grouping and one generated Cargo command per workspace;
- duplicate package/artifact/image detection;
- registry address/service alias validation;
- latest/version ref ordering;
- credential pair validation;
- bounded concurrency actually overlaps, never exceeds the limit, attempts all jobs, and aggregates multiple failures;
- publish-result formatting.

Add a small two-component Cargo workspace fixture and a Dagger test module close to `wash`. Public-boundary tests cover:

- auto-discovery and explicit selection;
- one exported artifact per selected package;
- workspace batch build success;
- fallback build success for an intentionally incompatible config;
- anonymous publication to an in-session disposable OCI registry;
- both version and latest manifests existing for both components;
- optional registry service versus external registry input validation;
- repeated publication actually executing rather than being layer-cached.

Do not assert undocumented engine scheduling timing. Concurrency-limit behavior belongs in the injected runner unit test; Dagger traces are used for end-to-end evidence.

### 7. Migrate and verify Typewriter locally

Before release, test Typewriter against the local rewritten module reference. Remove its WIT workflow, update environment examples, regenerate code, and run its Dagger validation. Use a disposable registry for publish tests; do not push test tags to production.

### 8. Release and pin

Follow `.agents/skills/daggerverse-release/SKILL.md` and the commit skill:

1. Commit only Daggerverse changes with a breaking `wash` scope.
2. Create `wash/v0.7.0`.
3. Push `main` and the new tag.
4. Record the full release commit hash.
5. Change Typewriter's wash source and pin to the released tag/hash.
6. Regenerate Typewriter after the remote pin is in place.
7. Leave Typewriter changes uncommitted/unpushed unless the user separately requests that release step.

## Files to modify

### Daggerverse

| Path | Change |
| --- | --- |
| `wash/main.go` | Full build/publish rewrite, new public API, workspace planner, cache graph, bounded publisher; remove WIT and legacy API. |
| `wash/main_test.go` | Replace WIT-centric tests with planner, validation, cache-key, ref, concurrency, and aggregate-error tests. |
| `wash/go.mod` / `wash/go.sum` | Add direct concurrency dependency and retain only parsers/runtime dependencies used by the rewrite. |
| `wash/README.md` | Document workspace-first behavior, fallback conditions, cache namespace, new API/CLI arguments, concurrency, failure semantics, and migration breakage. |
| `wash/test-wit-fetch.sh` | Delete; WIT is no longer part of this module. |
| `wash/testdata/**` | Add a minimal multi-package Cargo/wasmCloud fixture plus fallback fixture. |
| `wash/tests/**` | Add the Dagger public-API integration test module and generated metadata. |
| `wash/dagger.gen.go`, `wash/internal/**` | Regenerate after the public schema changes. |
| `README.md` | Update the catalog description so wash is build/publish-only and workspace-aware. |
| `docs/wash-workspace-publisher-refactor.md` | Keep this approved plan as the implementation record. |

### Typewriter

| Path | Change |
| --- | --- |
| `.dagger/wit.go` | Delete the WIT synchronization/check workflow. |
| `dagger.json` | Pin `wash/v0.7.0` and its full release commit. |
| `.env.example` | Replace the old wash block with `WASH_CACHENAMESPACE=typewriter-backend`, a hostname-valued `WASH_PUBLISHCOMPONENTS_REGISTRY`, optional `WASH_PUBLISHCOMPONENTS_REGISTRYSERVICE=tcp://...`, and no `REGISTRYHOSTNAME`; preserve repository and credential examples. |
| `.dagger/dagger.gen.go`, `.dagger/internal/**`, `.dagger/go.mod`, `.dagger/go.sum` | Regenerate/update for the breaking toolchain API and removed WIT calls. |

## Reuse

- Reuse `.wash/config.yaml` as the discovery source and artifact-path source of truth.
- Reuse the current latest-plus-version policy and credential secret handling, but move them behind stricter validation.
- Reuse the current artifact copy-out principle: target caches are accelerators, never final output ownership.
- Reuse `gopkg.in/yaml.v3` for wash config parsing and a TOML parser for Cargo manifests.
- Reuse Dagger `Workspace.Directory` filtering with gitignore support, while replacing broad component-at-a-time source graphs.
- Reuse Typewriter's existing `rootDir=/backend` intent and local registry service, translated to the new constructor/publish inputs.
- Follow Dagger's documented Go concurrency pattern, modified to collect all results rather than cancel on first error.

The old `componentContainer`, per-component target key, marker-file `Directory.Sync`, WIT lock merger, and public toolchain container are intentionally not reused.

## Open questions and assumptions

### Resolved decisions

- Workspace-first build with an independent correctness fallback.
- Breaking public API is allowed.
- Public functions are only `BuildComponents` and `PublishComponents`.
- Always publish `latest` plus an optional version tag.
- Every ref is an independent bounded publish job; default limit is 8.
- Finish all ref jobs and aggregate all failures.
- Derive Rust from Cargo metadata; pin wash in the module.
- Require a caller-owned cache namespace.
- Remove WIT from wash and remove Typewriter's WIT workflow entirely.
- Include Typewriter migration.
- Release as `wash/v0.7.0`, push it, and update Typewriter's pin.
- Performance acceptance uses trace invariants plus reported timings, not a hard wall-clock threshold.

### Assumptions

1. Typewriter keeps its current standard `cargo build --target wasm32-wasip2 --release`, empty `build.env`, `wit.skip_fetch: true`, and workspace-level artifact paths, so all eight publishable components qualify for one fast-path group.
2. Typewriter Cargo package names remain suitable OCI image names and currently match component directory basenames.
3. Typewriter's `workspace.package.rust-version` maps to an official `rust:<version>-bookworm` image tag. The explicit image override is the escape hatch if it does not.
4. wash 2.5.2 has official Linux amd64/arm64 artifacts suitable for checksum pinning. If upstream does not publish checksum files, implementation will compute the artifact digests once from the official release assets and commit those constants.
5. A partial registry update is acceptable when one or more refs fail, provided every ref is attempted and the aggregate error identifies successes and failures.
6. Typewriter working-tree migration is left uncommitted/unpushed after pinning unless separately requested.

No assumption currently blocks implementation.

## Steps

1. [ ] Record baseline Typewriter cold, unchanged-warm, and one-source-change build traces/timings with wash v0.6.3 in a disposable worktree.
2. [ ] Replace the wash constructor/public schema with required cache namespace plus `BuildComponents` and `PublishComponents`.
3. [ ] Implement canonical component discovery, path validation, config parsing, Cargo workspace/package/rust-version planning, and collision checks.
4. [ ] Implement checksum-pinned wash installation and Rust-image derivation/override.
5. [ ] Implement manifest-only dependency fetch layers and schema-versioned Cargo registry/git/locked workspace-target caches.
6. [ ] Implement one selected-package Cargo invocation per compatible workspace and copy all artifacts to `/out`.
7. [ ] Implement correctness-first `wash build` fallback groups.
8. [ ] Implement registry validation, optional service binding, latest/version refs, push-only cache busting, bounded terminal evaluation, and aggregate reporting.
9. [ ] Remove legacy single-component/container APIs and all WIT code/tests.
10. [ ] Rewrite unit tests and add fixture/public-boundary Dagger integration tests with a disposable registry.
11. [ ] Regenerate the wash Dagger SDK, format, tidy dependencies, and update module/root documentation.
12. [ ] Migrate Typewriter locally: remove `.dagger/wit.go`, rewrite the root `.env.example` wash block with the exact new cache namespace/registry/service variables shown above, temporarily use the local wash module, and regenerate its Dagger SDK.
13. [ ] Run Typewriter cold, unchanged-warm, and one-source-change traces/timings against the rewrite; inspect compile and push overlap invariants.
14. [ ] Commit the verified Daggerverse rewrite, create `wash/v0.7.0`, push `main` and the tag, and capture the full commit hash.
15. [ ] Replace Typewriter's local wash reference with `wash/v0.7.0` plus the exact pin, regenerate, and rerun end-to-end validation.
16. [ ] Show the final uncommitted Typewriter diff and report baseline/new timings, trace evidence, release tag/hash, and push status.

## Verification

### Daggerverse static and unit verification

From the Daggerverse repository:

```bash
dagger develop --mod ./wash
dagger -m ./wash functions
# Run the module's Go/unit test entry through a Dagger session or its test module.
dagger -m ./wash/tests call all
```

Also run formatting/tidying through the module's supported development container/session and verify no generated diff remains after a second `dagger develop`.

### Integration verification

The wash test module must:

1. Build the two-component fixture through auto-discovery and explicit selection.
2. Export deterministic `<cargo-package>.wasm` artifacts.
3. Start an in-session disposable registry service.
4. Publish both fixture components with a version tag.
5. Query the registry and confirm both `:<version>` and `:latest` manifests exist for every component.
6. Repeat the same publication and confirm the push nodes execute again while build nodes remain cached.
7. Exercise the fallback fixture.
8. Exercise aggregate failure handling and bounded runner behavior through deterministic injected jobs.

### Typewriter performance and cache verification

Use the same machine/engine and a disposable Typewriter worktree for baseline and rewritten runs. Record Dagger trace URLs and elapsed times for:

1. Cold `BuildComponents` of all eight publishable components.
2. Immediate unchanged repeat.
3. A one-file edit in one component followed by another build.
4. Publish to a disposable registry with version plus latest tags.

Acceptance invariants:

- The rewritten cold trace contains exactly one Cargo build invocation for the eight compatible Typewriter packages.
- The unchanged repeat hits the Dagger build layer cache.
- After a one-component source edit, Cargo uses the same workspace target cache and does not compile all third-party/shared dependencies from scratch.
- Every final artifact comes from the shared build's `/out` copy, not directly from the cache mount.
- Version and latest pushes share the same built artifact.
- Ref push spans visibly overlap and no more than 8 are active at once.
- A second publish executes push spans again despite compile/build cache hits.
- All 16 expected Typewriter refs (8 components × version/latest) exist in the disposable registry.
- Multiple simulated push failures are all reported after every job was attempted.

Report before/after cold, warm, and changed-source timings, but do not fail solely on a fixed duration threshold.

### Typewriter migration verification

```bash
cd /Users/thijs/Bestanden/TypewriterV1
dagger develop
dagger functions
```

Then verify:

- no generated/reference use of `WitFetch`, `WitSync`, or `WitCheck` remains;
- `dagger.json` points to `wash@wash/v0.7.0` and the exact release commit;
- the new required cache namespace is `typewriter-backend`;
- external registry address and optional Dagger registry service are distinct inputs;
- Typewriter's remaining Dagger functions/checks compile;
- `git diff` contains only the intended migration and generated changes.

### Release verification

```bash
git show --no-patch --oneline wash/v0.7.0
git rev-parse wash/v0.7.0
git status --short --branch
```

Confirm `main` and `wash/v0.7.0` exist on origin, report the full pin hash, and leave Typewriter uncommitted/unpushed unless separately authorized.
