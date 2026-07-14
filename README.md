# smith (Toolsmith)

A durable, portable, *safe* home for agent-authored and agent-consumed CLIs: a
thin convention + trust gate over [mise](https://mise.jdx.dev). See
`toolsmith-spec.md` for the full spec; this README records what's built and
the decisions taken.

## Status: Phases 0–1 complete (single-machine)

| Command | State |
|---|---|
| `smith init` | ✅ activates `store/trusted/bin` on PATH via `mise.local.toml`; verifies quarantine is off PATH |
| `smith add <name>[@ver]` | ✅ resolves/installs via mise (aqua backend checksums apply), lands at Tier 2 quarantine, sha256-pins each artifact |
| `smith list` / `status` | ✅ `--json` for the machine path |
| `smith run <name> [args]` | ✅ re-verifies the sha256 pin **before every execution**; Tier 0/2 forced into sandbox, refuses to run if no sandbox mechanism exists |
| `smith promote` | ✅ minimal: interactive-TTY-gated review → moves quarantine→trusted, symlinks onto PATH, provenance records reviewer |
| `smith demote <spec> --reason` | ✅ off PATH immediately, back to quarantine, reason recorded |
| `smith verify` | ✅ provenance hash-chain + all sha256 pins + PATH hygiene + trusted-bin symlink containment |
| `smith new <name>` | ✅ scaffolds an agent-friendly CLI draft (bash / python-stdlib) in `$SMITH_HOME/drafts/` with self-test, SKILL.md stub, draft manifest |
| `smith register <name>` | ✅ runs the draft's self-test **inside the sandbox**; failure blocks registration; pass → Tier 0 quarantine + printed promotion request |
| `smith publish` | Phase 2 (signed index) |

Phase 0 acceptance not yet checked: **two-machine reproduction** (needs the
Linux box; `mise.toml` + the index are designed for it but it's unverified).

## Layout

The **code repo** (this directory) and the **runtime home** are separate.
`$SMITH_HOME` (default `~/.toolsmith`) holds:

```
index/tools/<name>/<version>.yaml   one manifest entry per tool@version (unsigned until Phase 2)
store/trusted/<name>/<version>/     Tier 1 binaries
store/trusted/bin/                  symlink farm — the ONLY directory ever on PATH
store/quarantine/<name>/<version>/  Tier 0 + 2 — never on PATH, only via `smith run`
provenance/log.jsonl                append-only, hash-chained (each entry commits to prev line's sha256)
sandbox/profiles/default.sb         Seatbelt profile (written by `smith init`)
```

`.gitignore` in `$SMITH_HOME` excludes `store/` so the home can become the
Phase 2 index repo without ever committing binaries.

## Decisions taken (spec §10 open questions)

- **Language:** Go single-binary. Only deps: `yaml.v3`, `x/term`. Go itself is
  pinned via this repo's `mise.toml` (dogfooding).
- **Backend:** mise's registry (aqua-backed) via `mise install` — smith never
  downloads binaries itself. `mise install` does not activate anything;
  smith copies executables out of mise's store into quarantine.
- **Sandbox (provisional, revisit in Phase 1):** macOS `sandbox-exec` with a
  Seatbelt profile (deprecated-but-functional per spec §10) — no network, writes
  confined to CWD+TMPDIR. Linux: bubblewrap with `--unshare-net`, read-only
  root, tmpfs /tmp. **No sandbox available ⇒ refuse to run**, never fall back.
- **Activation:** `smith init` writes `mise.local.toml` (merged by mise with
  `mise.toml`) adding `env._.path = [<trusted bin>]`, then `mise trust`. The
  init command verifies both invariants against actual `mise env` output.
- **Sandbox (confirmed for Phase 1):** the Seatbelt/bwrap approach also runs
  every authored tool's self-test, so registration itself can't be an escape
  vector. Confirmed empirically: a self-test writing to `$HOME` fails.
- **Authored-tool versioning (§10):** both — semver in `manifest.yaml` (human
  intent) + sha256 pins in the index (reproducibility). Re-registering the
  same version is refused; edit ⇒ bump.
- **Python deps deferred:** authored python tools are stdlib-only. PEP 723 /
  uv scripts need cache-write access inside the sandbox, and a shared writable
  package cache is a poisoning vector into trusted context. Needs a dedicated
  design (per-run cold cache vs pre-warmed read-only cache) before allowing.
- **SKILL.md pairing (§10):** every authored tool gets the stub (cheap at
  authoring time); discovery-token cost is deferred until skills are actually
  exported to sessions — the stub lives with the tool in the store, pinned as
  a doc, never executed.

## Trust-gate properties verified end-to-end (this machine)

- Quarantined tool escape attempts blocked by sandbox: `touch $HOME/...` →
  `Operation not permitted`; `curl` → cannot resolve host; CWD write allowed.
- Tampered binary (pin mismatch) → `smith run` refuses; `smith verify` flags it.
- `smith promote` with stdin = pipe or `/dev/null` → refused (real `isatty`
  check via `x/term`; `os.ModeCharDevice` alone is bypassable by `/dev/null`).
- Provenance chain tamper (edited line) → `verify` reports the break.
- Demote removes the PATH symlink before touching anything else.

**Honest limitation:** the TTY gate stops *accidental* agent promotion (agent
shells are non-interactive), not an adversarial agent with pty tooling. The
load-bearing gate is the human review itself; provenance records who claimed
to review. This matches the spec's intent — promotion review means reading
the source.

## Build & test

```sh
mise exec -- go build -o smith ./cmd/smith
mise exec -- go test ./...
```

## Usage sketch

```sh
smith init                 # in a project dir; then `mise activate` your shell or use `mise exec`
smith add jq@1.7.1         # → Tier 2, quarantined
smith run jq '.foo' < in   # sandboxed
smith promote jq@1.7.1     # human, interactive: review → Tier 1, on PATH
smith demote jq@1.7.1 --reason "CVE-..."
smith verify --json

# authoring loop (cli-creator, see skills/cli-creator/SKILL.md)
smith new mytool --runtime bash --desc "what it does"
$EDITOR ~/.toolsmith/drafts/mytool/{mytool,selftest.sh,SKILL.md}
smith register mytool      # sandboxed self-test → Tier 0 + promotion request
```
