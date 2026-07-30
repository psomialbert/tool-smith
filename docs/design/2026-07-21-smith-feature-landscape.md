# smith — feature landscape & prioritized backlog

Date: 2026-07-21
Status: brainstorm output (not a design spec). Each item still needs its own
brainstorm → spec before implementation.

A prioritized menu of candidate features for smith (v2: tools as git repos,
adopted per project behind a trust gate; `new → use → install`). Weighted across
four lenses the maintainer cares about — trust & security, ecosystem & reach,
dev & agent ergonomics, team & org adoption — plus novel ideas. Effort is a
rough T-shirt size (S · M · L), not an estimate.

A recurring signal: the strongest near-term candidates are the ones that
**productize what a real session already did by hand** (self-healing project
state, upgrading a pinned tool, publishing a tool repo).

---

## Recommended priority order

| # | Feature | Lens | Effort | Why here |
|---|---------|------|--------|----------|
| 1 | `smith doctor` | ergonomics / trust | S–M | Turns the reactive heal/migrate logic (stale activation, tracked `mise.local.toml`, drifted sha, non-functional sandbox) into one proactive command. Immediate, low-risk. |
| 2 | `smith upgrade` + `smith diff` | ergonomics | M | Biggest daily gap — upgrading a pin is manual today; the source diff *is* the review surface. |
| 3 | `smith publish` | ecosystem | S–M | Structurally kills the "adopted by local path / never pushed" failure class. |
| 4 | `smith use --mise <backend>` | ecosystem | M | Adopt third-party CLIs (pup, rg…) per-project; huge reach, smith already sits on mise. |
| 5 | `smith verify --frozen` | trust | S | Cheap, strong guarantee that `.smith/` matches the reviewed commit; CI gate. |
| 6 | Capability manifest + enforcement | trust | M | Makes the trust gate substantive (declare + enforce network/fs/env); feeds the review report. |
| 7 | Sign the allowlist (ed25519) | trust / org | M | Closes the "project PR silently widens org policy" hole. |
| 8 | Smith Lens (agent tool-index) | innovative | M | Agent-first discovery; builds on the SKILL-describes-the-tool separation. |

**Watch-list (bigger / riskier, revisit later):** sandbox v2, tool composition,
org catalog, monorepo support, usage-aware suggestions, execution receipts.

---

## 🔒 Trust & security

- **`smith verify --frozen`** `S` — re-hash every vendored tool against its pin;
  fail on drift/tampering. CI gate guaranteeing `.smith/` matches the reviewed
  commit.
- **Capability manifest + enforcement** `M` — manifest declares
  `network`/`fs-paths`/`env`/`subprocess`; `smith review` surfaces them and the
  sandbox enforces exactly what's declared.
- **Sign the allowlist (ed25519)** `M` — org signs policy so a project PR can't
  silently widen it.
- **Upstream provenance check** `M` — at adopt time, verify the vendored bundle
  sha matches the upstream tag (+ optional git / GitHub attestation).
- **Sandbox v2** `L` — Landlock (Linux) / tighter Seatbelt / microVM (macOS).

## 🌐 Ecosystem & reach

- **`smith use --mise <backend>`** `M` — adopt third-party CLIs via mise
  ubi/go/aqua, pinned per-project. Answers the "standardize pup across the team"
  question.
- **`smith publish`** `S–M` — scaffold → repo → push → tag → topic in one flow.
- **More runtimes** `M` each — Node/Deno (TS tools), Rust (compiled like go).
- **Org tool catalog** `M` — a curated repo of approved tools that `smith
  search` reads (complements the allowlist).
- **Monorepo support** `M` — workspace-level `.smith/` so packages share adopted
  tools.

## 🛠️ Dev & agent ergonomics

- **`smith doctor`** `S–M` — one proactive check: stale activation, tracked
  `mise.local.toml`, missing/inactive mise, non-functional sandbox, drifted sha,
  uninstalled tools.
- **`smith upgrade` + `smith diff`** `M` — bump a pin to a newer tag, re-run
  selftest, re-vendor, show a source diff for review.
- **`smith use --ref <branch>`** `S` — adopt from a branch/commit while
  iterating pre-tag.
- **`--json` everywhere + richer `smith list`** `S` — agents drive smith
  reliably; list shows "upgrade available", grants, last-verified.

## 👥 Team & org adoption

- **`smith use --pr`** `M` — open the adoption PR via `gh` with a templated body
  (what/why/capabilities/selftest result).
- **Adoption review report** `M` — auto-generated reviewer checklist
  (capabilities, source size, selftest assertions, provenance).
- **`smith bootstrap`** `S` — one command in a fresh clone: install all pinned
  tools + activate (discoverable onboarding).
- **Offline / org source mirror** `M–L` — for CI and air-gapped environments.

## 💡 Brand-new / innovative

- **Smith Lens — live agent tool-index** `M` — smith maintains a compact,
  always-fresh index of *this project's* adopted tools + their contracts, loaded
  like a meta-skill. Solves agent discovery; leverages the
  SKILL-describes-the-tool separation.
- **Usage-aware suggestions** `M` — smith notices an agent hand-writing the same
  ad-hoc script repeatedly and suggests "make this a tool?"; flags unused
  adoptions to prune.
- **Execution receipts** `M` — tools emit signed `inputs-hash → outputs-hash`
  receipts for reproducible pipelines (ties to cerebrum's determinism gate).
- **Tool composition** `L` — a tool declares dependence on another smith tool;
  smith resolves the graph. Powerful but risks dependency-hell.

---

## Next step

Pick one feature to take through a full brainstorm → spec → plan cycle. The top
three (`doctor`, `upgrade`/`diff`, `publish`) are the highest value-to-effort
and each closes a failure mode observed in real use.
