# No dependency source-builds at adopt time (python-uv)

Date: 2026-07-28
Status: approved design, pre-implementation

## Problem

Adopting a `python-uv` tool runs its dependencies' build backends (arbitrary
`setup.py` / PEP-517 hooks) **before a human reviews anything**, in two places:

- `uvLockScript` runs `uv lock --script` **fully unsandboxed** — the real
  environment, network, and whole filesystem. This is the worse of the two.
- `warmUVCache` runs `uv sync --script` sandboxed, but with `Network: true` and
  the sandbox binds `/` read-only, so a build backend can read `~/.ssh`,
  `~/.aws`, etc. and exfiltrate them over the granted network.

Both `uv lock` and `uv sync` build a source distribution when a dependency has
no wheel / no PEP-658 metadata — that build is the arbitrary-code channel. The
env is already sanitized and the tool's own script is never executed at adopt
time (`uv sync`, not `uv run`); the gap is specifically dependency *builds*.

## Goal / core property

Adopting a python-uv tool never executes its dependencies' build backends.
Resolution and install use wheels + PEP-658 metadata only. A source build is
refused unless the tool explicitly opts in via its manifest, and that opt-in is
surfaced to the reviewer.

Non-goal (deliberately deferred): hiding `$HOME` from the warm/lock sandbox
("Approach B"). The opt-in build path keeps today's read surface — made
explicit rather than silent. B can be layered on later for the opt-in case.

## Design

### 1. Sandbox the lock step and forbid source builds by default

- Wrap `uvLockScript` in `sandbox.Command` (workDir = clone dir so it can write
  `<script>.lock`, `Perms{Network: true}`, sanitized env) — mirroring
  `warmUVCache`. `uv lock` is currently the one unsandboxed step; this closes
  it.
- Add `--no-build` to both the `uv lock` and `uv sync` invocations when the tool
  has not opted into source builds. With no build backend running, the default
  adopt path executes no arbitrary dependency code, so the sandbox's full-read
  mount is no longer a meaningful exfil surface for that path.

### 2. Opt-in flag: `sandbox: { build: true }`

- New boolean field `Build` on `scaffold.SandboxSpec` (manifest key
  `sandbox.build`). It is a **build-policy flag, not a mount grant**: it gates
  whether `--no-build` is passed. It does NOT get seatbelt/bwrap enforcement the
  way `write`/`network` do, because it changes the uv command line, not the
  sandbox profile.
- When `build: true`, source builds are allowed (omit `--no-build`).
- Single source of truth: `manifestPerms` copies `m.Sandbox.Build` into a new
  `Build bool` field on `sandbox.Perms`. That field is read only by
  `capabilitySummary` (surfacing) and by the uv-arg construction (the
  `allowBuild` decision) — it is never referenced by `seatbeltProfile` or
  `linuxBwrapArgv`, so it does not affect the sandbox profile or its cache hash.
- To make `perms.Build` available to the uv lock/sync steps, compute
  `perms, err := manifestPerms(m)` **once, early** (right after the manifest
  load + rename guard, before the runtime switch), instead of after it. This is
  a pure, side-effect-free move; `runSelfTest` uses the same `perms`.
- Surfaced in `capabilitySummary` as: "builds dependency source at adopt time
  (runs dependency build code with network + local-file read — review it)", so
  the residual exposure only exists when a human sees the flag and adopts
  anyway.

### 3. Failure UX

When `--no-build` makes resolution fail (a dependency is sdist-only for the
platform), map uv's error to a clear hint:

> "adopting <tool> needs to build a dependency from source, which is disabled by
> default — the tool must ship wheels, or declare `sandbox: { build: true }` in
> its manifest (which lets its dependencies' build code run with network +
> local-file read at adopt time; review it before adopting)."

### 4. Testable seam

Extract uv argument construction into a pure function:

```go
// uvArgs builds the argv for a uv subcommand over a PEP-723 script, forbidding
// source builds unless the tool opted in.
func uvArgs(subcmd, scriptRel string, allowBuild bool) []string
```

so `uvLockScript`/`warmUVCache` both call it (each passing `perms.Build`). Tests
assert `--no-build` is present when `!allowBuild` and absent when `allowBuild`,
with no network needed. Plus tests that `manifestPerms` maps `sandbox.build`
into `perms.Build` and that `capabilitySummary` names it. A real sdist-build
integration test needs PyPI network access and is skipped in CI (matches the
existing `TestUseInstallPythonUV` skip pattern).

### 5. Docs and sync-check

- `SECURITY.md`: rewrite the build/warm-steps section — default is now "no
  dependency source builds at adopt time; `uv lock` is sandboxed"; document the
  `build:` opt-in and its exposure.
- `skills/cli-creator/SKILL.md`: document `sandbox: { build: true }` in the
  sandbox section.
- `README.md`: add `build:` to the documented `sandbox:` keys.
- `CLAUDE.md`: note the new manifest key and that `build:` is a build-policy
  flag (not a mount grant, so no seatbelt/bwrap wiring).
- `v2ConfigKeys` is untouched: `sandbox.build` is a manifest key, not a
  `config.FileConfig` key.

## Risk

A tool with a legitimately sdist-only dependency stops adopting until it opts in
— intended, and the error message tells the author exactly how. Wheels-based
tools (the common case) are unaffected.

## Out of scope

- Hiding `$HOME` from the sandbox (Approach B).
- The other two documented-but-unfixed findings (allowlist signature rollback;
  `install.sh` release authenticity).
