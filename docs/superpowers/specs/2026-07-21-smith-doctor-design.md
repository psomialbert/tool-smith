# smith doctor — design spec

Date: 2026-07-21
Status: approved design, ready for implementation planning.

## Problem

smith already contains logic to detect and repair broken project/machine state —
stale/foreign `mise.local.toml` activation, missing gitignore entries, vendored
source drift, a non-functional sandbox, retired v1 home cruft — but it only
fires **reactively**, as a side effect of `smith install` / `use` / `migrate`.
Users hit these problems by surprise (e.g. a peer's `smith install` erroring on a
committed `mise.local.toml`, a stale activation path, vendored-sha drift). There
is no single command to answer "is this project/machine healthy, and how do I fix
it?"

## Goal

A `smith doctor` command that proactively diagnoses machine- and project-level
health, reports findings in a glanceable form (and `--json`), and optionally
applies safe, reversible repairs with `--fix`. It gates CI on real problems via
its exit code.

Non-goals: replacing `install`/`use`/`migrate`; network operations during `--fix`;
mutating the user's git index/history.

## Decisions (locked)

1. **Fix model:** diagnose by default (read-only, CI-safe); `--fix` applies safe,
   reversible repairs only. Mirrors `smith migrate`'s backup-then-remove posture.
2. **Scope:** context-aware. Machine checks always run; project checks run when
   the cwd is inside a project with a `.smith/` dir.
3. **Output/exit:** grouped human report + `--json`; exit `0` when there are no
   ERROR findings (warnings tolerated), non-zero when any ERROR remains
   (recomputed after `--fix`).
4. **Architecture:** a check registry over shared pure `diagnose` + `apply`
   halves, refactored out of the existing side-effecting helpers so there is one
   source of truth per check.
5. **Git boundary:** `--fix` never runs `git`. A tracked `mise.local.toml` is
   reported with a `git rm --cached` hint but not auto-executed.

## Architecture

New `cmd/smith/doctor.go`.

```go
type Severity int   // OK, WARN, ERROR
type Scope    int   // Machine, Project

type Finding struct {
    Check    string      // stable id, e.g. "activation"
    Severity Severity
    Message  string      // what's wrong, or the OK line
    FixHint  string      // exact command/action to fix (shown when not fixed)
    Fixable  bool        // --fix can repair it safely
    Fixed    bool        // set after a successful --fix apply
}

type Check struct {
    Name     string
    Scope    Scope
    Diagnose func(dc docCtx) Finding
    Apply    func(dc docCtx) error   // nil => not auto-fixable
}
```

`docCtx` carries `config.Paths`, the resolved project dir (or ""), and the loaded
`ProjLock`. `cmdDoctor`:

1. Resolve context: always Machine; add Project if a `.smith/` dir is found at or
   above cwd.
2. Select checks whose Scope is in context.
3. Run each `Diagnose` → collect `Finding`s.
4. If `--fix`: for each `Fixable && Severity != OK` finding, run `Apply`, then
   re-`Diagnose` to confirm; set `Fixed` or downgrade to ERROR on failure.
5. Render (human grouped, or `--json`).
6. Exit non-zero iff any ERROR remains.

Flags: `--fix`, `--json`, optional `--machine` / `--project` to restrict scope.

## Check catalog

### Machine (always)

| id | severity if bad | fixable | notes |
|----|-----------------|---------|-------|
| `mise-present` | ERROR | no | mise on PATH; hint to install |
| `sandbox-functional` | ERROR | no | run the fails-closed no-op sandbox probe; if it fails, adoption is unsafe |
| `v1-home-cruft` | WARN | yes | retired v1 dirs / dead config keys in `SMITH_HOME`; apply = existing `migrate` backup+strip |

### Project (when in a project)

| id | severity if bad | fixable | notes |
|----|-----------------|---------|-------|
| `activation` | ERROR | yes | `mise.local.toml` missing, foreign, or stale `_.path`; apply = `applyActivation` |
| `gitignore` | WARN | yes | missing `.smith/bin/`, `.smith/cache/`, `mise.local.toml` entries; apply = add them |
| `tracked-mise-local` | WARN | no | `mise.local.toml` is git-tracked; hint `git rm --cached` (git boundary) |
| `vendored-sha` | ERROR | no | bundle hash of `.smith/tools/<name>/` ≠ pin (drift/tamper); hint to re-`use` or restore |
| `installed` | WARN | yes | pinned but not materialized on `.smith/bin`; apply = per-tool materialize |
| `mise-trust` | WARN | yes | project mise config not trusted; apply = `mise trust` |
| `skill-trigger` | WARN | no | a vendored `SKILL.md` frontmatter `description` is empty or still the scaffold placeholder (`TODO: sharpen the trigger`) — an un-sharpened trigger means the tool won't be discovered |

`vendored-sha` and `skill-trigger` stay report-only. `vendored-sha` because
auto re-vendoring needs a network clone (outside the `--fix` safety envelope);
`skill-trigger` because writing a good trigger is human judgment — the check can
detect a placeholder/empty trigger mechanically, but not that a trigger fails to
mention a newly-added command. That un-automatable part is covered by the
cli-creator "Changing a tool" checklist, not by doctor.

## Refactor scope (shared diagnose/apply)

- `ensureActivation` → `diagnoseActivation` + `applyActivation`
- `ensureProjectGitignore` → `gitignoreMissing() []string` + apply
- sandbox probe → promote the test helper into `sandbox.Functional(p) error`,
  used by doctor and tests
- `bundleHash` (already pure) → doctor compares against the pin
- `cmdMigrate` → diagnose (list present v1 artifacts + dead keys) + apply
  (existing backup+strip)

Existing callers (`install`, `use`, `migrate`) switch to the `apply` halves so no
behavior changes and no logic is duplicated.

## Output

Human (default):

```
smith doctor — machine + project (cerebrum-artificiale)

machine
  ✓ mise present · ✓ sandbox functional (seatbelt)
  ⚠ v1 home cruft in ~/.toolsmith        → smith doctor --fix
project
  ✓ activation → .smith/bin
  ✗ vendored drift: projviz sha ≠ pin    → smith use github:…/projviz@v0.3.2
  ⚠ det-canary pinned, not installed     → smith install

3 ok · 2 warnings · 1 error
```

`--json`:

```json
{
  "context": ["machine", "project"],
  "project": "cerebrum-artificiale",
  "findings": [
    {"check":"activation","scope":"project","severity":"ok","message":"…","fixable":false,"fixed":false}
  ],
  "summary": {"ok": 3, "warn": 2, "error": 1}
}
```

Exit: `0` if no ERROR, `1` otherwise (recomputed after `--fix`).

With `--fix`, repaired findings render as `fixed:` and the summary/exit reflect
the post-fix state.

## Testing

- Per-check unit tests: seed a temp project/home in each failure state, assert
  `Diagnose` severity, run `Apply`, assert repaired and re-diagnose is OK.
- Integration: a project seeded with several issues → `doctor` (assert findings +
  exit 1) → `doctor --fix` (assert repaired + exit 0).
- `--json` shape test.
- Regression: existing `install`/`use`/`migrate` tests still pass after the
  diagnose/apply refactor.

## Surfaces to update (coupling checklist)

- dispatch `switch` + `usage()` in `cmd/smith/main.go`
- `README.md` command list
- `CLAUDE.md` (note doctor as the proactive health command)

## Rollout

Ship in the next minor release (v2.6.0 — new command). No breaking changes; the
refactor is internal.
