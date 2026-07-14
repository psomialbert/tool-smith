# smith (Toolsmith)

A durable, portable, *safe* home for agent-authored and agent-consumed CLIs: a
thin convention + trust gate over [mise](https://mise.jdx.dev). See
`toolsmith-spec.md` for the full spec; this README records what's built and
the decisions taken.

## Status: Phases 0–3 complete (all spec phases)

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
| `smith promote` (full) | ✅ authored tools: blob is packed, sha256-pinned, and pushed to the shared transport **before** the trust transition — promotion refused if no transport or push fails |
| `smith publish` | ✅ ed25519-signs the manifest set (`index/index.sig`), commits index+provenance in the `$SMITH_HOME` git repo, pushes if a remote exists; refuses if a Tier 1 entry still has a machine-local source |
| `smith fetch <name>[@ver]` | ✅ machine B: installs from the index alone — blob (or mise re-install) fetched, blob + every artifact checksum verified; Tier 1 requires a **valid index signature**, otherwise lands quarantined at Tier 2 |
| `smith verify` | ✅ now also validates the index signature and every per-machine provenance chain |
| `smith add` hardening | ✅ Phase 3: verification level decided before any download — `gh-attestation` (SLSA provenance verified over the exact downloaded archive) > `aqua-registry` (curated checksums via mise) > `tofu` (refused unless `--allow-unverified`); level recorded in index + provenance and shown at promotion review |

Two-machine acceptance is verified against a real Linux environment via
`scripts/linux-e2e.sh` (privileged ubuntu container): clones the live GitHub
index, validates the signature, reproduces the external tool version via
mise, and proves bwrap blocks write/network escapes. A physical Linux box
would additionally exercise `bwrap` outside a container, but the code paths
are identical.

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
- **Blob transport (§10):** pluggable, two backends. `github-release`
  (recommended): assets on a rolling `smith-blobs` release in a **private**
  repo via `gh`. `local-dir`: any synced folder. Configure per machine in
  `$SMITH_HOME/config.yaml`:
  `transport: {kind: github-release, repo: you/smith-index}` or
  `{kind: local-dir, path: /path/to/synced}`.
- **Index signing:** stdlib ed25519, no cosign/minisign install needed —
  smith is both signer and verifier of a *private* index; interop wasn't
  buying anything. `index.sig` = signature over a canonical digest of every
  entry YAML. The private key lives only in `keys/` (git-ignored, 0600);
  `smith.pub` travels with the index repo — trust-on-first-clone, so check
  the fingerprint out-of-band when setting up a new machine.
- **Platform-keyed entries:** `index/tools/<name>/<version>/<platform>.yaml`
  (`any` for scripts, else `GOOS-GOARCH`) — external binaries hash
  differently per arch, so pins are per platform; authored scripts pin once.
- **Per-machine provenance:** `provenance/<hostname>.jsonl` — independent
  hash chains merge cleanly through git where one shared appended file would
  conflict and break.
- **Attestation (§10 / Phase 3):** GitHub native attestations via
  `gh attestation verify` against the kept mise download archive
  (`MISE_ALWAYS_KEEP_DOWNLOAD=1`) — verifying the extracted binary would miss
  the point, since attestations cover release archives. A tampered archive
  shows as "no attestations found" (digest lookup), which is why the checksum
  layer stays mandatory and attestation is a recorded upgrade, not the only
  gate. Deeper cosign/SLSA checks defined in aqua registry entries are mise's
  job: install `cosign`/`slsa-verifier` and mise applies them during install.
- **Cross-machine trust rule:** trust travels via the signed index only. A
  Tier 1 script fetches straight to trusted on machine B after signature +
  checksum verification. An external tool fetched onto a *different platform*
  than the reviewed one has different bytes — it lands at Tier 2 and needs a
  local (fast, provenance-informed) promotion.

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

# cross-machine (machine A)
$EDITOR ~/.toolsmith/config.yaml   # transport: {kind: github-release, repo: you/smith-index}
smith promote mytool@0.1.0 # human review; blob pushed to transport first
smith publish              # sign manifest set, commit + push index repo
# (machine B)
git clone <index remote> ~/.toolsmith && $EDITOR ~/.toolsmith/config.yaml
smith fetch mytool@0.1.0   # signature + checksums verified → Tier 1, on PATH
```
