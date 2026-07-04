---
name: daggerverse-release
description: Commit, tag, push, and pin Daggerverse module releases. Use when user asks to publish or release any module in this repository, bump version, tag release, push release, or pin modules such as gitops or wash in dependent repositories.
---

## Objective

Release any Daggerverse module from this repository and update dependent repositories that pin that module.

Behavior:

- Load commit skill first from `$HOME/.agents/skills/commit/SKILL.md`.
- Follow commit skill process for discovery, grouping, staging, commit message quality, and safety checks.
- Resolve target module before committing, tagging, or pinning.
- Commit only changes in this repository when releasing module changes.
- Create next `<module>/vX.Y.Z` tag after successful commit.
- Push `main` and new tag to origin.
- Update dependent repositories only when they already pin target module or user names them.
- Report commit hash, tag, push status, and target pin hash.

## Module Scope

Default repository root:

```text
$HOME/Bestanden/daggerverse
```

Valid module:

- Directory under repository root with `dagger.json`.
- Module name from `dagger.json` field `name`.
- Module path from directory name.
- Module name and directory name should match. Ask user before release if they differ.

Known modules today:

- `gitops`: `$HOME/Bestanden/daggerverse/gitops`
- `wash`: `$HOME/Bestanden/daggerverse/wash`

Resolve module from this order:

1. User supplied module name or path.
2. Current working directory if it is a module directory or inside one.
3. Changed files when all release changes are under one module directory.
4. Ask user when multiple modules changed or target is unclear.

Expected module tag format:

```text
gitops/v0.5.0
wash/v0.1.0
```

Expected remote branch:

```text
main
```

## Dependent Repositories

Known repositories to check after release:

- TypeWriter: `$HOME/Bestanden/TypeWriter/features/v1/dagger.json`.
- Infrastructure v3: `$HOME/Bestanden/Infrastructure/v3/dagger.json`.

Each `dagger.json` can have a `toolchains` array entry with `source` and `pin` fields:

```json
{
  "name": "gitops",
  "source": "github.com/seamlezz/daggerverse/gitops@gitops/v0.5.0",
  "pin": "faae777c513465df8f74c86b60b87765a2008ff0"
}
```

For target module, update only matching toolchain entries:

- `name` equals target module name.
- Or `source` contains `github.com/seamlezz/daggerverse/<module>@`.

Update both `source` tag and `pin` full commit hash to new release.

If known dependent repository has no matching entry, do not edit it. Report it as skipped.

If user supplies extra dependent repositories, apply same matching rule unless user explicitly asks to add new entry.

## Required Process

### Step 1: Load commit rules

Read commit skill before doing commit work:

```text
$HOME/.agents/skills/commit/SKILL.md
```

Apply its restrictions and process.

### Step 2: Resolve target module

Identify target module and verify it has `dagger.json`:

```bash
pwd
git status --short --branch
git diff --name-only
git diff --cached --name-only
```

Read target module metadata:

```bash
bat <module>/dagger.json
```

Ask user if module cannot be resolved safely.

### Step 3: Inspect repo state

Run commit skill required checks from repository root:

```bash
git status
git diff
git diff --cached
git log --oneline -10
```

Inspect current tags for target module only:

```bash
git tag --list '<module>/v*' --sort=-version:refname
```

### Step 4: Commit changes

Use commit skill grouping rules.

Prefer commit scope equal to module name, for example `gitops` or `wash`, because each module has independent release tags.

Breaking API changes need `!` plus body:

```text
BREAKING CHANGE: describe caller visible API change.
```

### Step 5: Pick next version

Use latest `<module>/vX.Y.Z` tag.

Version bump rules:

- No prior tag: use `v0.1.0` unless user requested another version.
- Breaking API change before `v1.0.0`: bump minor, for example `v0.4.0` to `v0.5.0`.
- New compatible feature before `v1.0.0`: bump minor.
- Bug fix only: bump patch.
- Docs, style, or internal chore only: ask user whether release tag should be created.

If unsure which bump applies, ask user.

### Step 6: Tag and push

After commit succeeds:

```bash
git tag <module>/vX.Y.Z
git push origin main <module>/vX.Y.Z
```

Verify:

```bash
git status --short --branch
git show --no-patch --oneline <module>/vX.Y.Z
git rev-parse HEAD
```

### Step 7: Update dependent repositories

For each dependent repository listed above, plus any user supplied dependent repository:

0. Read `dagger.json`.
0. Find matching toolchain entry for target module.
0. Skip repository when no matching entry exists.
0. Edit `source` tag and `pin` hash to new release.
0. Show resulting diff for target `dagger.json`.

Do not stage, commit, or push dependent repository changes unless user explicitly asks.

Do not add new toolchain entries unless user explicitly asks.

### Step 8: Report pin info

Final response must include:

- Module name.
- Commit subject and short hash.
- New tag.
- Full pin hash from `git rev-parse HEAD`.
- Push result for Daggerverse repository.
- Dependent repository result for each checked repository, edited or skipped.

## Hard Boundaries

Never do these unless user explicitly asks:

- Create tags outside target module prefix.
- Reuse existing tag name.
- Modify unrelated files in dependent repositories.
- Add target module to dependent repositories that do not already pin it.
- Release multiple modules in one tag.
