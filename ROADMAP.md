# Roadmap

Spec Phases 0–3 shipped (see README). This plans what comes after, informed
by gaps found while building and by ecosystem investigation (July 2026).
Versions are themes, not promises; order within a release is by risk.

## Ecosystem facts that shape the plan

- **mise now ships `mise.lock`**: cross-platform checksums (one file, many
  platforms) *and* provenance verification — SLSA, cosign, minisign, GitHub
  attestations — performed at `mise lock` time, trusted at install time.
  Full support for aqua/http/github/gitlab backends. This overlaps with the
  index's external-tool pinning and with our hand-rolled `gh attestation
  verify`; §2's "call it, don't reimplement" says delegate (v0.3).
- **`sandbox-exec` remains deprecated with no successor API**: Apple offers
  no non-deprecated way to apply Seatbelt profiles to arbitrary unsigned
  processes. Community direction is microVMs (Apple `containerization`
  framework, Lima). Seatbelt keeps working today; we need an exit ramp, not
  a rewrite (v0.5).
- **uv script environments are cached per dependency-spec** and rerun
  near-instantly; `UV_CACHE_DIR` is redirectable. A **per-tool** cache dir
  kills the shared-cache poisoning vector that blocked python deps (v0.4).

## v0.2 — Operational hardening (do first)

- ~~**Sanitize the environment for sandboxed runs.**~~ **Shipped** (was a
  bug): Tier 0/2 runs and self-tests get an allowlist env
  (PATH/TMPDIR/LANG/LC_*/TERM/TZ) + scratch HOME; explicit per-invocation
  forwarding via `smith run --pass-env`. Still open for v0.4: declaring
  required env in the tool *manifest* so passthrough is part of promotion
  review rather than per-invocation.
- ~~**`smith sync`.**~~ **Shipped**: `git pull --no-rebase` the index +
  reconcile the local store — tools demoted/removed upstream come off PATH
  and move to quarantine immediately (provenance-logged), and uninstalled
  Tier 1 entries are reported as fetch candidates (never auto-fetched).
  Append-only provenance logs use a `merge=union` `.gitattributes` so
  per-host `<hostname>.jsonl` files never conflict on pull.
- ~~**Provenance completeness.**~~ **Shipped for `publish` + sync-demote**;
  every trust transition now writes a chain entry. Still open: a `demote`
  triggered purely by a local index edit outside the commands.
- ~~**Tier 0 garbage collection.**~~ **Shipped**: `smith gc
  [--older-than 30d] [--yes]` — dry-run by default, removes only unpromoted
  Tier 0 versions past the age cutoff plus drafts with no registered
  version; never touches Tier 1/2; provenance-logged.
- ~~**Index schema version field** + migration hook.~~ **Shipped**:
  `Entry.Schema` (current = 1), `migrate()` runs on load and backfills
  pre-versioning entries (Schema 0 → platform `any`); `Save` always stamps
  the current version.
- ~~**CI**~~ **Shipped** (`.github/workflows/ci.yml`): gofmt + vet + build +
  `go test ./...` on ubuntu + macos (bwrap installed on Linux, sandbox-exec
  built into macOS), Go provisioned via mise-action. The privileged
  container e2e stays a local pre-release step. *Unverified until the code
  repo has a GitHub remote + first push.*
- **Dogfood milestone** (done): `calc`, a safe AST-based expression
  evaluator, authored end-to-end through `smith new`/`register` and living
  at Tier 0 in the real index awaiting human promotion — the authoring loop
  proven on a non-trivial tool, including self-test assertions that reject
  code execution.

## v0.3 — Lean on mise.lock (started)

- ~~`smith add` drives `mise lock --platform` and captures cross-platform
  checksums.~~ **Shipped**: add generates a per-tool `mise.lock` under
  `index/tools/<name>/<version>/` covering the platforms in
  `config.yaml` `platforms:` (default: this machine). `ManifestDigest` now
  hashes every file under `index/tools` (not just `.yaml`), so the lockfile
  is part of the signed, tamper-evident index — verified live: editing a
  locked checksum makes `smith verify` fail the signature check.
- ~~Revisit TOFU policy.~~ **Shipped**: backends that record a download
  checksum in mise.lock (`ubi`/`github`/`gitlab`/`http`) graduate from
  refuse-by-default to `mise-checksum` level; `aqua` stays `aqua-registry`;
  version-only backends (`asdf`/`cargo`/`go`/`npm`/`pipx`/`vfox`) remain
  TOFU, refused unless `--allow-unverified`. Policy is `attest.Allowed()`.
- ~~**Cross-platform external *fetch*.**~~ **Shipped**: `smith add` publishes
  metadata-only **stubs** for the other configured platforms (mise→GOOS-GOARCH
  mapping), so a fetch-only machine finds the tool. `smith fetch` installs an
  external stub via the committed `mise.lock` (`mise install` verifies the
  download against the signed per-platform checksum), then records the
  extracted-binary hashes as **local pins** in the git-ignored store —
  resolving the integrity tension: the *archive* checksum in the signed
  lockfile is the cross-machine anchor, the per-machine extracted pin is
  local-only state that `run`/`verify` check. A fetch-only machine never
  writes the signed index, so its signature stays valid. Verified end-to-end
  in a Linux/arm64 container against a macOS-authored index.
- Delegate attestation more deeply to mise's provenance verification (read
  the `provenance` field mise records in the lockfile) — still open; today
  we record backend class + our own `gh attestation verify`.

## v0.4 — Authoring depth

- ~~**Python with dependencies**~~ **Shipped**: `smith new --runtime
  python-uv` scaffolds a PEP 723 script. `smith register` runs `uv lock`
  (hash-locked `<name>.py.lock` — the review surface), generates a
  self-locating launcher, warms a tool-local cache in a **network-sandbox**
  (sanitized env, confined writes — so dep download/build can't reach
  secrets), and self-tests **offline** under the strict sandbox. Runs use
  `uv run --offline` from the tool cache (the launcher mirrors it to a
  writable temp, since the store is read-only under the sandbox). The
  launcher resolves its real path (following the Tier-1 PATH symlink), so it
  works at Tier 0/2 and Tier 1. python-uv tools are platform-specific (the
  cache holds platform wheels), like Go. *Verified end-to-end: a tool using
  `six` registers, runs offline sandboxed (`{"six":"1.16.0"}`), fails with
  the cache removed (no network fallback), promotes, and runs via the Tier-1
  PATH launcher. Supply-chain note: deps execute at run; the lockfile makes
  the tree reproducible, not safe — the dep tree is part of promotion review.*
  *(Still open: wheel-only enforcement to avoid sdist install-time code;
  cross-platform fetch re-warm.)*
- ~~**Go template**~~ **Shipped**: `smith register` compiles main.go offline
  (GOPROXY=off, CGO_ENABLED=0, -trimpath); Go tools are platform-specific
  (platform=GOOS-GOARCH), main.go pinned as a source doc. *(Still open:
  cross-compile + per-platform blobs at promote; today it's host-platform.)*
- ~~**Per-tool sandbox profiles**~~ **Shipped**: manifest `sandbox: {network:
  true}` grants egress to a Tier 0/2 tool's `smith run`; the grant is in the
  pinned manifest and surfaced at review ("NETWORK egress requested").
  Seatbelt profile generated per permission-set; linux drops `--unshare-net`.
  Self-tests always run strict (least-trusted moment). *(Named-host egress
  is future; today it's all-or-nothing network.)*
- ~~**`smith upgrade <name>`**~~ **Shipped**: authored → reseeds the draft
  from the trusted source at a patch-bumped (or `--version`) version to edit
  + re-register; external → adds the newer version via the full add pipeline
  (Tier 2, own review). Old version stays until demoted. *(Still open: show a
  diff vs the promoted version at review time.)*
- ~~**Skill export**~~ **Shipped**: `smith promote` offers to install the
  tool's SKILL.md into `~/.claude/skills/<name>/` (config `skills.dir`,
  `skills.export: ask|always|never`), and `smith demote` removes it — so
  discovery follows trust. Resolves the spec §10 discovery question: export
  at trust time, not authoring time. Provenance logs a `skill-export` event.

## v0.5 — Sandbox v2

- **macOS exit ramp**: keep Seatbelt as default; add an opt-in microVM
  backend (Apple `containerization` / Lima) selected per tool or globally —
  same `sandbox.Command` seam, so tools don't notice. Decision forced only
  if Apple removes `sandbox-exec`.
- ~~**Escape-test suite as a first-class check**~~ **Shipped**: `smith verify
  --sandbox` runs the hostile-tool battery (write escape → $HOME blocked, CWD
  write works, network denied by default, permitted under a grant) on the
  *active* mechanism. Verified on darwin (Seatbelt) and linux (bwrap).
  *(Could add more probes: env leak, /proc & keychain.)*
- Linux: evaluate Landlock as a bwrap complement for unprivileged
  environments where user namespaces are disabled.

## v1.0 — Self-hosting

- **smith distributes smith**: tagged releases with GitHub attestations;
  `smith add smith` works from the public repo at `gh-attestation` level;
  the index carries its own tooling. Dogfood-complete.
- ~~**Key lifecycle**~~ **Shipped**: `smith key list|rotate|add|revoke`.
  Rotate installs a fresh key, keeps the outgoing one authorized for its
  window, re-signs; `index/authorized_keys` is a multi-key trust set so a
  second machine can publish without copying the primary key. Trust rests on
  repo access + the TOFU smith.pub root (authorized_keys deliberately not in
  the signed digest — see sign.go).
- Docs pass; cut v1.0.0 when a stranger to this repo can go
  clone → init → author → promote → publish → fetch on machine B without us.

## Still explicitly out (unchanged from spec §4/§9)

Public contribution hub, multi-user governance/RBAC, GUI, auto-publish.
The prompt-injection → malware-distribution risk hasn't moved; a public hub
remains a distinct product decision, not a roadmap item.
