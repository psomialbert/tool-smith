# smith (Toolsmith)

A durable, portable, **safe** home for the CLIs an agent writes and reuses — a
thin convention + trust gate over [mise](https://mise.jdx.dev).

An agent that authors its own tools has nowhere trustworthy to keep them:
a script lives untracked on one machine (lost next session) or gets pasted
back into context every time, and pulling someone else's tool is a
supply-chain event an agent can be prompt-injected into. smith gives
agent-authored and agent-consumed CLIs a reproducible per-project
environment, a path from throwaway script to discoverable capability, and a
**trust boundary so nothing runs unvetted**.

The novel bit is the closed loop: **author → sandbox → self-test →
human-gated promotion → durable, versioned, discoverable, portable
capability**. Everything version/PATH-related delegates to mise; smith adds
the convention, the sandbox, and the trust gate.

## Trust tiers

Every tool smith can run has a tier. This is the whole point.

| Tier | Meaning | Where it lives | How it runs |
|---|---|---|---|
| **0 — authored** | agent-written, pending review | `store/quarantine/` | sandboxed, only via `smith run` |
| **2 — external** | pulled from outside, pending review | `store/quarantine/` | sandboxed, only via `smith run` |
| **1 — trusted** | passed human-gated promotion | `store/trusted/` | on PATH, unsandboxed |

Tier 0/2 tools are **only** reachable through `smith run`, which sandboxes
them (no network, writes confined to CWD+TMP, sanitized env) and re-checks
their sha256 pin before every execution. Only Tier 1 lands on PATH, and a
tool becomes Tier 1 **only** after a human reads its source and promotes it
in an interactive terminal — a passing self-test is necessary but never
sufficient. Trust is revocable (`smith demote`) and every transition is
recorded in an append-only, hash-chained provenance log.

## Requirements

- [mise](https://mise.jdx.dev) — version/PATH management (smith is a wrapper over it)
- A functional OS sandbox to run Tier 0/2 tools: macOS Seatbelt (built in) or
  Linux `bwrap` with unprivileged user+net namespaces (see *Sandbox host
  requirements* below). smith **fails closed** without one.
- `git` and `gh` for cross-machine publishing (optional until you go multi-machine)

## Install

`smith` is a single binary; it needs [mise](https://mise.jdx.dev) on PATH at
runtime. Pick whichever fits:

```sh
# From a clone (builds via mise+go):
make install                       # -> ~/.local/bin/smith  (override with PREFIX=…)

# On any machine with `gh` authenticated (downloads the release binary):
sh scripts/install.sh              # auto-detects OS/arch, installs to ~/.local/bin
#   or the one-liner, no script needed:
gh release download v1.0.1 --repo psomialbert/tool-smith \
  --pattern "smith-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" \
  -O ~/.local/bin/smith && chmod +x ~/.local/bin/smith
```

All three paths also install the **cli-creator authoring skill** to
`~/.claude/skills/cli-creator/` (override with `SKILLS_DIR` / `SMITH_SKILLS_DIR`),
so authoring agents discover the CLI contract without extra setup.

Prebuilt binaries (darwin/linux × arm64/amd64) plus the skill are attached to
each [release](https://github.com/psomialbert/tool-smith/releases). Ensure
`~/.local/bin` is on PATH. For agents: install once per machine, then confirm
`smith version` and `mise --version` both work.

## Quickstart

```sh
smith init                     # set up ~/.toolsmith + put the trusted store on PATH here
smith add jq@1.7.1             # external tool → Tier 2 (quarantined)
echo '{"a":1}' | smith run jq '.a'   # runs sandboxed
smith review jq@1.7.1          # read it, then:
smith promote jq@1.7.1         # interactive, human-gated → Tier 1, on PATH

# author your own (see skills/cli-creator/SKILL.md)
smith new greet --runtime bash --desc "what it does"   # bash | python | go | python-uv
$EDITOR ~/.toolsmith/drafts/greet/{greet,selftest.sh,SKILL.md}
smith register greet           # sandboxed self-test → Tier 0 + promotion request
smith review greet && smith promote greet@0.1.0

# go multi-machine
$EDITOR ~/.toolsmith/config.yaml   # transport: {kind: github-release, repo: you/smith-index}
smith publish                  # sign the index + push
#   on machine B:
git clone <index remote> ~/.toolsmith
smith fetch greet@0.1.0        # signature + checksums verified → Tier 1, on PATH
```

`SMITH_HOME` (default `~/.toolsmith`) is the runtime home and doubles as the
signed index git repo; the code repo (this directory) is separate. See
`toolsmith-spec.md` for the full spec and `ROADMAP.md` for what's next.

## Adopting tools per project (v2 — the direction)

The personal signed index is being **retired** in favor of a package-manager
model: **a tool is a git repo** (public, private-org, or embedded), and you
**adopt** it into a project the same way you'd add any dependency — a
reviewable commit.

```sh
smith search <query>           # discover tool repos (GitHub topic: smith-tool)
smith use <owner/repo>[@<ver>] # self-test sandboxed, vendor source into
                               #   .smith/tools/<name>/, pin in .smith/tools.yaml
git add .smith && git commit   # the adoption — your team reviews the tool's
                               #   source in the PR, like any dependency
smith install                  # realize .smith/tools.yaml onto the project PATH
```

- **Trust is per-project.** A tool vendored + pinned in the (reviewed)
  `.smith/tools.yaml` is trusted for that project and goes on PATH (via mise);
  the **PR review is the promotion gate**. `smith use` runs the tool's
  self-test sandboxed before vendoring, so unreviewed code is contained at
  adopt time; the human reviews the vendored source in the diff.
- **Discovery ≠ trust.** `smith search` finds repos by topic; found tools are
  only ever trusted through the adoption PR.
- **Integrity.** `smith install` re-hashes the vendored bundle against the
  pin and refuses on mismatch (go.sum-style). `.smith/bin/` is generated and
  git-ignored; `.smith/tools.yaml` + `.smith/tools/<name>/` are committed.
- A **teammate who clones the project** and runs `smith install` gets the
  exact same tool — proven end-to-end in `scripts/adopt-e2e.sh`.

Currently interpreted runtimes (bash/python); the older index commands
(`add`/`promote`/`publish`/`fetch`/`key`) still exist but are on the way out.
An optional signed **allowlist** (which repos an org permits adopting) is a
planned policy layer on top. See `ROADMAP.md`.

## Using smith with agents

smith is built for the pattern where an **agent authors and reuses its own
CLIs** but a **human owns the trust decision**. The division of labor is the
whole safety model — keep it:

| Agents may | Humans only |
|---|---|
| `new`, `register` (→ Tier 0), `add` (→ Tier 2), `run` (sandboxed), `review` (read), `fetch`, `list`, `verify` | `promote`, `demote`, `publish`, `key` |

An agent's job ends at **emitting the promotion request** — it never promotes.
`smith promote` is gated on an interactive terminal, so a non-interactive
agent shell can't promote even by accident; but the rule is also social, so
state it in your agent's instructions. The load-bearing gate is a human
reading the source (`smith review`) before promoting; a passing self-test is
necessary, never sufficient.

To make your agents smith-aware, add something like this to your agent
instructions (e.g. `~/.claude/CLAUDE.md` for Claude Code):

```markdown
# smith (agent tool store)
`smith` keeps reusable CLIs behind a trust gate. If a tool would help and
doesn't exist, author one: `smith new <name> --runtime bash|python|go|python-uv`,
implement it, then `smith register <name>` (sandboxed self-test → Tier 0).
Run tools with `smith run <name>` (Tier 0/2 run sandboxed). You may `smith add
<ext>@<ver>` for external tools. Do NOT promote/publish — emit the promotion
request and stop; a human reviews and promotes. See the cli-creator skill for
the agent-friendly CLI contract.
```

The authoring contract (structured `--json` output, non-interactive, stable
flags, self-test) lives in [`skills/cli-creator/SKILL.md`](skills/cli-creator/SKILL.md);
point authoring agents there. Promoted tools' own `SKILL.md`s are exported to
`~/.claude/skills/<name>/` on promotion, so future sessions discover them.

## Command reference

All spec phases (0–3) plus the v0.2–v1.0 roadmap are implemented and tested
(unit + a real two-machine run). Status of each surface:

| Command | State |
|---|---|
| `smith init` | ✅ activates `store/trusted/bin` on PATH via `mise.local.toml`; verifies quarantine is off PATH |
| `smith add <name>[@ver]` | ✅ resolves/installs via mise (aqua backend checksums apply), lands at Tier 2 quarantine, sha256-pins each artifact |
| `smith list` / `status` | ✅ `--json` for the machine path |
| `smith run <name> [args]` | ✅ re-verifies the sha256 pin **before every execution**; Tier 0/2 forced into sandbox, refuses to run if no sandbox mechanism exists |
| `smith review <name>` | ✅ pre-promotion review. On a TTY: an interactive TUI (syntax-highlighted, arrow/PgUp-PgDn/g-G navigation, `n`/`p` between files, `space` to select line ranges, `c` to comment → emits an agent-pasteable report on quit). Re-checks bytes vs pins first. Also `--edit` (`$VISUAL`→`$EDITOR`→`config.yaml editor:`), `--paths`, `--plain` (dump; the default when piped). Config `review.theme` (dark/light/mono), `review.color`, `review.default` (tui/plain/edit/paths for the no-flag default). Footer always shows mode-appropriate keys. Read-only |
| `smith promote` | ✅ interactive-TTY-gated review → moves quarantine→trusted, symlinks onto PATH, provenance records reviewer; offers to export the tool's SKILL.md into `~/.claude/skills/<name>/` so sessions discover it (config `skills.export`) |
| `smith demote <spec> --reason` | ✅ off PATH immediately, back to quarantine, reason recorded; removes the exported skill so discovery stays in sync with trust |
| `smith verify` | ✅ provenance hash-chain + all sha256 pins + PATH hygiene + trusted-bin symlink containment |
| `smith new <name>` | ✅ scaffolds an agent-friendly CLI draft — `bash` / `python` (stdlib) / `go` (compiled) / `python-uv` (PEP 723 + deps) — with self-test, SKILL.md stub, manifest. Go/python-uv register produces platform-specific tools; python-uv locks deps with hashes, warms a tool-local cache, runs offline |
| `smith register <name>` | ✅ runs the draft's self-test **inside the sandbox**; failure blocks registration; pass → Tier 0 quarantine + printed promotion request |
| `smith upgrade <name> [--version X]` | ✅ v0.4: authored → reseeds the draft from the trusted source at a patch-bumped (or given) version to edit + re-register; external → adds the newer version (Tier 2, own review). Old version stays until demoted |
| `smith promote` (full) | ✅ authored tools: blob is packed, sha256-pinned, and pushed to the shared transport **before** the trust transition — promotion refused if no transport or push fails |
| `smith publish` | ✅ ed25519-signs the manifest set (`index/index.sig`), commits index+provenance in the `$SMITH_HOME` git repo, pushes if a remote exists; refuses if a Tier 1 entry still has a machine-local source |
| `smith fetch <name>[@ver]` | ✅ machine B: installs from the index alone — blob (or mise re-install) fetched, blob + every artifact checksum verified; Tier 1 requires a **valid index signature**, otherwise lands quarantined at Tier 2 |
| `smith verify` | ✅ now also validates the index signature and every per-machine provenance chain; `--sandbox` runs the live escape battery (write confinement, network deny/grant) |
| `smith key list\|rotate\|add\|revoke` | ✅ v1.0: multi-key trust set (`index/authorized_keys`) + rotation with a validity window; lets a second machine publish. Trust rooted in repo access + TOFU `smith.pub` |
| per-tool sandbox grant | ✅ manifest `sandbox: {network: true}` grants egress to a Tier 0/2 tool's `smith run`; surfaced at review; default is deny-all |
| `smith add` hardening | ✅ Phase 3: verification level decided before any download — `gh-attestation` (SLSA provenance verified over the exact downloaded archive) > `aqua-registry` (curated checksums via mise) > `tofu` (refused unless `--allow-unverified`); level recorded in index + provenance and shown at promotion review |
| `smith sync` | ✅ v0.2: `git pull` the index + reconcile — tools demoted/removed upstream come off PATH and into quarantine immediately (provenance-logged); uninstalled Tier 1 entries reported as fetch candidates. Propagates revocation across machines |
| `smith gc` | ✅ v0.2: dry-run by default; `--older-than 30d --yes` removes only unpromoted Tier 0 versions past the cutoff + unregistered drafts; never touches Tier 1/2 |
| `smith add` cross-platform pins | ✅ v0.3: generates a per-tool `mise.lock` (checksums for the platforms in `config.yaml` `platforms:`), now part of the signed index digest; checksum-bearing backends (`ubi`/`github`/`gitlab`/`http`) graduate from TOFU-refusal to `mise-checksum`. Also publishes metadata-only stubs for the other platforms |
| `smith fetch` (external, cross-platform) | ✅ v0.3: materializes a stubbed external on a fresh platform via the committed `mise.lock` (checksum-verified), recording extracted-binary hashes as **local pins** in the git-ignored store. Fetch-only machines never touch the signed index, so its signature stays valid; `run`/`verify` check index pins when present, else local pins |

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
index/tools/<name>/<version>/<platform>.yaml   one manifest entry per tool@version@platform
index/tools/<name>/<version>/mise.lock          cross-platform checksums for external tools
index/smith.pub, index/authorized_keys, index/index.sig   signing trust set + signature
store/trusted/<name>/<version>/     Tier 1 binaries
store/trusted/bin/                  symlink farm — the ONLY directory ever on PATH
store/quarantine/<name>/<version>/  Tier 0 + 2 — never on PATH, only via `smith run`
provenance/<hostname>.jsonl         append-only, hash-chained per machine (each entry commits to prev line's sha256)
keys/smith.key                      private signing key — git-ignored, never leaves this machine
sandbox/profiles/                   generated Seatbelt profiles
config.yaml                         transport, platforms, review, skills, editor settings
```

`.gitignore` in `$SMITH_HOME` excludes `store/`, `keys/`, and `drafts/`, so
the home can be the signed index git repo without committing binaries, the
private key, or work-in-progress.

## Sandbox host requirements

Tier 0/2 tools run only sandboxed, so a functional sandbox is required to run
(or self-test) them: macOS Seatbelt (`sandbox-exec`, built in) or Linux
bubblewrap (`bwrap`) with **unprivileged user + network namespaces enabled**.
smith fails closed where that's unavailable — e.g. GitHub Actions' Ubuntu
runners forbid the netns setup `--unshare-net` needs, so Tier 0/2 tools can't
run there (CI still builds/vets/tests the pure logic and exercises Seatbelt on
macOS; the sandbox-execution tests skip when no functional sandbox is found).
Escaping this class of restricted host is the v0.5 microVM/Landlock work.

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
- Sandboxed runs (and self-tests) get a **sanitized environment**: only
  PATH/TMPDIR/LANG/LC_*/TERM/TZ pass through, HOME points at a scratch dir,
  tokens never reach Tier 0/2 code. Explicit forwarding:
  `smith run --pass-env NAME[,NAME] <tool>` (visible in the command line, by
  design). Tier 1 runs unsandboxed with the full environment.
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

## Not in scope (v1)

Per spec §4/§9: no public contribution hub, no multi-user governance/RBAC, no
GUI, no auto-publish. The prompt-injection → malware-distribution risk means a
public hub is a deliberate future product decision, not a feature toggle.
Provenance yes; access control no. Single operator.
