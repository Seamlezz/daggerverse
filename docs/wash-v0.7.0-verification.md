# wash v0.7.0 verification

Typewriter was used as the representative eight-component Cargo workspace. Timings are observations from one development machine and are not release thresholds.

## Legacy baseline

`wash/v0.6.3` built the eight components with eight independent `wash build` executions and eight Cargo completion events.

- Elapsed: 311.89 seconds
- Trace: https://dagger.cloud/gabber235/traces/97ae8dc5d5aeeb4757d1d5be532e5d90

## v0.7.0 cache invariants

The released module emitted one command with eight repeated package selectors:

```text
cargo build --locked --target wasm32-wasip2 --release \
  --package auth-callout \
  --package auth-sentinel \
  --package auth-typewriter-permissions \
  --package organization-members \
  --package organization-roles \
  --package service-identity \
  --package service-registration \
  --package user-organization
```

After changing the output of `auth-sentinel`, Cargo rebuilt only that workspace crate:

- Elapsed: 71.37 seconds
- Cargo: `Finished release ... in 44.14s`
- Trace: https://dagger.cloud/gabber235/traces/7eafb5f47c2e2ef8b62a1416da86f57e

After restoring the source, one run rebuilt only `auth-sentinel`; the following stable warm run emitted no `Compiling` lines:

- Stable warm elapsed: 80.23 seconds
- Trace: https://dagger.cloud/gabber235/traces/b48b86023d56a44da782cd3f8cef7d7b

The source mutation was restored after measurement.

## Publication invariants

The public-boundary integration suite published two components, each with `v1` and `latest`, to an in-session disposable registry. Its trace contains four terminal `wash oci push` executions completing in the same time window, and registry tag-list requests verified both tags for both components.

- Trace: https://dagger.cloud/gabber235/traces/34965a3380d86326680dbdedcb3ed3ad

A separate final release-path check published `v0.7.0-final` and `latest` with checksum-pinned wash 2.5.2 binaries:

- Trace: https://dagger.cloud/gabber235/traces/9bfe750cee5a1669394c228bb4b81862
