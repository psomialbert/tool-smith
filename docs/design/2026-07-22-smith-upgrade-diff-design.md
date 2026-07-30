# smith upgrade + smith diff — design spec

Date: 2026-07-22
Status: approved design, ready for implementation.

## Problem

Upgrading an adopted tool is manual today: you re-type `smith use <repo>@<newver>`,
having first figured out what the new version even is, with no way to preview
what would change. This session hit it twice (projviz v0.2→v0.3.2, det-canary
v0.1.0→v0.1.1) — each a hand-run `smith use`.

## Goal

- `smith upgrade [tool][@ver]` — bump one or all adopted tools to a newer version,
  re-running the sandboxed self-test gate and re-pinning (a reviewable commit).
- `smith diff <tool>[@ver]` — preview the vendored-source changes an upgrade
  would make, before doing it or during PR review.

## Decisions (locked)

1. **Target + scope:** auto-latest, bulk or single. `smith upgrade` (no arg) =
   all adopted tools → the latest semver tag of each source repo (discovered via
   `git ls-remote --tags`). `smith upgrade <tool>` = one. `@<ver>` = exact.
2. **Review model:** non-interactive. `smith upgrade` applies (re-vendor + re-pin);
   the git diff of the re-vendored `.smith/tools/<name>/` in the resulting commit
   is the review surface. `smith diff` is a separate preview.
3. **Upgrade scope:** re-pin only (mirrors `smith use`); prints "review the diff,
   then `smith install`". New source is reviewed before it's materialized on PATH.
4. **Architecture:** extract `cmdUse`'s clone→checkout→self-test→vendor→pin
   pipeline into a shared `adoptAtVersion`; `use` and `upgrade` both call it (one
   source of truth).
5. **Diffing:** reuse the existing `git` dependency (`git diff --no-index`); no Go
   diff library.
6. **Self-test failure policy:** keep the old pin, report the error, continue the
   other tools (never leave a half-upgraded pin).

## Architecture

### Shared adopt pipeline (refactor of cmdUse)

```go
type adoptResult struct{ Name, Version, Sha256, Dst string }

// adoptAtVersion clones clonedURL, checks out version ("" = repo's describe),
// loads the manifest, materializes+self-tests sandboxed, vendors the reviewable
// source into .smith/tools/<name>/, and updates+saves the pin. Returns what was
// adopted. This is cmdUse's body, extracted so upgrade reuses it verbatim.
func adoptAtVersion(p config.Paths, clonedURL, displaySource, version string) (adoptResult, error)
```

`cmdUse` becomes: parse args → `expandSource` → allowlist check → `adoptAtVersion`
→ print adoption message. Behavior unchanged.

### Version discovery

```go
// latestTag returns the highest vX.Y.Z semver tag advertised by the remote (or
// local repo path). Non-semver / pre-release tags are ignored.
func latestTag(clonedURL string) (string, error)   // git ls-remote --tags

// semverLess reports whether a < b for vX.Y.Z tags (numeric per component).
func semverLess(a, b string) bool
```

### `smith upgrade [tool][@ver] [--json]`

- Load `.smith/tools.yaml`. Targets = the named tool, or all tools if none named.
- For each target `t`:
  - resolve target version: `@ver` if given, else `latestTag(t.Source)`.
  - if target ≤ current pin → record "up to date", skip.
  - allowlist check on `t.Source` (defensive; it was allowed at adoption).
  - `adoptAtVersion(p, t.Source, t.Source, targetVer)` — the self-test gate runs.
    on error: record failure, **leave the old pin untouched**, continue.
  - on success: record `from → to`.
- Print per-tool summary + "review the diff, then `smith install`".
- `--json`: `{upgraded:[{tool,from,to}], uptodate:[tool...], failed:[{tool,error}]}`.
- Exit non-zero if any target failed.

### `smith diff <tool>[@ver] [--stat] [--json]`

- Resolve `<tool>` from the lockfile (error if absent). Target = `@ver` or
  `latestTag`.
- Shallow-clone `source@target` to a temp dir; copy the reviewable source files
  (`sourceFiles(runtime, entrypoint, name)`) into a mirror dir.
- Diff vendored vs target with git:
  - default: `git diff --no-index <vendored> <target>` (unified).
  - `--stat`: `git diff --no-index --stat`.
  - `--json`: parse `git diff --no-index --numstat` → `[{file, added, deleted}]`.
- Exit 0 whether or not there are differences (it's a report). Clone/tool errors
  are non-zero.

## Edge cases

- Tool not in lockfile → error naming it.
- Repo has no semver tags → error with a hint (`@<ver>` to pin explicitly).
- `@<older>` version → allowed (deliberate downgrade), printed as a downgrade note.
- Clone/network failure → error; the old pin is untouched.
- Local-path source → `latestTag` reads the local repo's tags.

## Testing

- `semverLess` ordering (incl. missing `v`, differing component counts).
- `latestTag` picks the highest from a local repo fixture with several tags.
- `adoptAtVersion` is exercised by both `use` and `upgrade` tests (local git repo
  with two tags): upgrade v1→v2 re-pins + re-vendors, sha changes.
- up-to-date target → no-op, pin unchanged.
- **self-test failure at the new version keeps the old pin** and reports failure.
- `smith diff` for a changed file: `--stat` names it, `--json` has add/del counts.

## Surfaces to update (coupling checklist)

- dispatch `switch` + `usage()` in `cmd/smith/main.go`
- `README.md` (upgrade + diff section)
- `CLAUDE.md` (note the shared `adoptAtVersion` so future changes touch one place)

## Rollout

Ship as v2.8.0 (two new commands, no breaking changes; the `cmdUse` refactor is
internal and behavior-preserving).
