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
- **CI**: GitHub Actions running `go test ./...` + `scripts/linux-e2e.sh`
  (minus the private-index step) on push. *(still open)*
- **Dogfood milestone** (done): `calc`, a safe AST-based expression
  evaluator, authored end-to-end through `smith new`/`register` and living
  at Tier 0 in the real index awaiting human promotion — the authoring loop
  proven on a non-trivial tool, including self-test assertions that reject
  code execution.

## v0.3 — Lean on mise.lock

- `smith add` drives `mise lock <tool> --platform <all-my-platforms>` and
  imports checksums for **every** platform into the index at add time — a
  tool reviewed once is pin-verified everywhere, removing today's
  "machine B re-adds and re-reviews externals" friction (the trust decision
  can then legitimately travel, since the reviewed *version*'s bytes are
  pinned per platform up front).
- Delegate attestation to mise's provenance verification where the backend
  supports it (aqua/http/github/gitlab); keep our direct
  `gh attestation verify` as fallback for backends mise doesn't cover.
  Record mise's verified provenance type in `verification`.
- Revisit TOFU policy: `ubi` gains checksum support in mise.lock, so it may
  graduate from refuse-by-default to checksum-level.

## v0.4 — Authoring depth

- **Python with dependencies**: PEP 723 template; at `smith register`, warm
  a per-tool `UV_CACHE_DIR` inside the tool's version dir (network allowed
  for that one controlled step), then all sandboxed runs use
  `uv run --offline` with writes allowed only to that tool-private cache.
  No shared mutable cache = no cross-tool poisoning path.
- **Go template**: compiled per platform; artifacts become per-platform
  blobs (index already supports this); `smith register` builds for the host,
  `smith promote` optionally cross-compiles + pushes all platform blobs.
- **Per-tool sandbox profiles**: manifest may request e.g. network egress to
  named hosts; the profile delta ships with the tool, is sha256-pinned as a
  doc, and is explicitly part of what the human reviews at promotion.
- **`smith upgrade <name>`**: new-version flow for authored tools (copy
  draft, bump, diff against promoted version shown at review) and externals
  (re-add + re-verify, previous version demoted on promote).
- **Skill export**: on promotion, offer to install the tool's SKILL.md into
  `~/.claude/skills/<name>/` (resolves the spec §10 discovery question:
  export at trust time, not authoring time).

## v0.5 — Sandbox v2

- **macOS exit ramp**: keep Seatbelt as default; add an opt-in microVM
  backend (Apple `containerization` / Lima) selected per tool or globally —
  same `sandbox.Command` seam, so tools don't notice. Decision forced only
  if Apple removes `sandbox-exec`.
- **Escape-test suite as a first-class check**: `smith verify --sandbox`
  runs the hostile-tool battery (env leak, write escape, network egress,
  /proc & keychain probes) on the *active* mechanism — the linux-e2e escape
  test generalized and runnable everywhere.
- Linux: evaluate Landlock as a bwrap complement for unprivileged
  environments where user namespaces are disabled.

## v1.0 — Self-hosting

- **smith distributes smith**: tagged releases with GitHub attestations;
  `smith add smith` works from the public repo at `gh-attestation` level;
  the index carries its own tooling. Dogfood-complete.
- **Key lifecycle**: rotation command (re-sign with new key, old pubkey
  retained for verification window), multiple authorized pubkeys so a second
  machine can publish without copying the primary key.
- Docs pass; cut v1.0.0 when a stranger to this repo can go
  clone → init → author → promote → publish → fetch on machine B without us.

## Still explicitly out (unchanged from spec §4/§9)

Public contribution hub, multi-user governance/RBAC, GUI, auto-publish.
The prompt-injection → malware-distribution risk hasn't moved; a public hub
remains a distinct product decision, not a roadmap item.
