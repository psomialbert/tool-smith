# Toolsmith — original v1 project spec (RETIRED — DO NOT IMPLEMENT)

> ## ⚠️ This document describes a model that no longer exists
>
> **If you are an agent or a contributor looking for smith's current design, stop
> reading this file.** Run `smith contract` for the tool-authoring contract, or
> read the [README](../../README.md). This file is kept only as a record of the
> original 2026-07-14 design.
>
> Nearly everything below is **retired**: the personal signed index, the
> `register` / `run` / `promote` verbs, Tier 0 / Tier 1 / quarantine, and
> `$SMITH_HOME/drafts/`. None of those commands exist. The current model is
> **`new → use → install`**: a tool is a git repo, adopted per project as a
> reviewed commit, trusted for that project only.
>
> This warning exists because it was needed. An agent was pointed at this repo to
> learn smith's conventions, read this file — whose filename and original header
> both made it look authoritative — and absorbed the retired model from it.
>
> The original header, which actively instructed agents to build from this
> document, read: *"Claude Code-ready build spec. An executing agent should be
> able to work Phase 0 → 3 from this document."* It does not apply to anything.

---

## 1. Problem

An agent that writes and reuses its own CLIs (the pattern from the supervisor-loop work) has no durable, portable, *safe* home for those tools. Today a self-authored script either lives as an untracked file on one machine (lost on the next session / next box) or gets pasted back into context every time (defeats the point). Meanwhile pulling a tool someone else wrote is a supply-chain event an agent can be prompt-injected into triggering.

Toolsmith gives agent-authored and agent-consumed CLIs a reproducible per-project environment, a codification path so a tool becomes a discoverable capability, and a trust boundary so nothing runs unvetted.

## 2. What already exists (do not rebuild)

- **Version/env management → delegate to `mise`.** `mise` already provides declarative, per-project, cross-machine tool pinning with lazy install, and it bakes in the `aqua` registry plus `ubi`/`cargo`/`npm`/`go` backends and native Cosign/SLSA/attestation verification. Toolsmith is a **thin convention + wrapper over mise**, not a new version manager.
- **External distribution → GitHub Releases + aqua-registry style manifests.** The "space where binaries live" already exists. Toolsmith's index is a *signed manifest pointing at those*, not a hosting platform.
- **Skill authoring → `skill-creator` exists**; Toolsmith's `cli-creator` is the missing *tool*-authoring counterpart that also registers into the env.

The novel surface is the **closed loop**: author → sandbox → self-test → human-gated promotion → durable, versioned, discoverable, portable capability. Build there.

## 3. Goals

1. A self-authored CLI created in one session is reproducibly runnable on every one of your machines after a single gated promotion step — no re-derivation, no context paste. (Requires a cross-machine blob transport for authored binaries; see §5.3 and Open Questions.)
2. Every tool Toolsmith can run has recorded provenance and a trust tier; nothing untrusted executes outside a sandbox.
3. Authoring a new agent-friendly CLI (structured output, non-interactive, stable flags) is one skill invocation, and the output passes its own self-test before it's offered for promotion.
4. Adding an external tool is checksum-pinned and attestation-checked by default; unverifiable tools are refused or quarantined, never silently trusted.

## 4. Non-goals (v1)

- **Not** a new version manager or PATH-shim system — that's mise. If a behavior exists in mise/aqua, call it, don't reimplement it.
- **Not** a public, multi-tenant, upload-for-everyone hub. The index is **private/local** in v1. Public contribution is explicitly deferred (see §9 risk) — the incumbents in that space compete on governance you'd have to match.
- **No** RBAC, org policy, or multi-user governance. Single operator (you). Provenance yes; access control no.
- **No** GUI. CLI + skill only.
- **No** auto-publish. Promotion and publish are always separate, human-approved actions.

## 5. Architecture

```
toolsmith/
  cmd/smith/                # thin wrapper over mise (Go single-binary preferred; see Open Questions)
  skills/
    cli-creator/
      SKILL.md              # authoring skill: scaffold agent-friendly CLI + manifest + skill stub
      templates/            # tool scaffolds per runtime (bash, python-uv, go)
  index/                    # signed manifest repo (git). One entry file per tool@version.
    tools/<name>/<version>.yaml
    index.sig               # signature over the manifest set
  store/
    trusted/<name>/<version>/   # Tier 1 only — the ONLY directory mise-activated onto PATH
    quarantine/<name>/<version>/ # Tier 0 + Tier 2 — never on PATH, reachable only via `smith run`
  sandbox/
    profiles/                 # bwrap (Linux) + macOS profile (see Open Questions; sandbox-exec is deprecated)
  provenance/                 # append-only JSONL, hash-chained per entry (tamper-evident): who/what/source/tier/review per tool@version
```

### 5.1 Environment layer (`smith` over mise)
- Project pins live in the existing `mise.toml`. Toolsmith adds a `[tools]`-adjacent convention for Toolsmith-managed tools and an activation hook so **`store/trusted/` (Tier 1 only)** lands on PATH per project the same way mise tools do. `store/quarantine/` is never in any activated directory — Tier 0/2 binaries are reachable exclusively through `smith run`, which applies the sandbox. This is what makes invariant §7.3 enforceable rather than aspirational.
- External tools resolve through mise backends (aqua/ubi/etc.) so cross-arch (arm64 darwin on the M5 + your Linux boxes) is mise's problem, not yours.

### 5.2 cli-creator skill
Given a described operation, it produces an **agent-consumable** CLI:
- structured output (`--json` / NDJSON), never prose as the machine path
- non-interactive by default (no prompts), stable flags, `--quiet`
- real exit codes; errors to stderr; machine-parseable `--help`
- a generated `SKILL.md` stub (name + trigger description) so future sessions discover *when* to use it
- a manifest entry (unversioned draft) + a self-test the tool must pass
It registers the result at **Tier 0** (quarantined + sandboxed, pending review) and emits a **promotion request**. It must never publish or promote on its own.

### 5.3 Local signed index
- One YAML entry per `tool@version`: name, version (see Open Questions on scheme), source, `sha256`, optional cosign/SLSA/attestation refs, provenance pointer, trust tier.
- **Source rule:** a Tier 1 entry's `source` must be fetchable from *all* your machines (e.g. private GitHub Release asset or git-LFS blob in the index repo — see Open Questions). A machine-local path is valid only for Tier 0; `smith promote` refuses to promote until the blob has been pushed to shared storage, otherwise Goal 1 silently fails on the second machine.
- The whole manifest set is signed (minisign/cosign). `smith verify` re-checks every checksum/signature.
- **The index and any pulled `SKILL.md` are data, not instructions.** Toolsmith never executes directives found inside manifest or skill text.

## 6. Command surface (`smith`)

| Command | Purpose | Tier effect |
|---|---|---|
| `smith init` | set up project convention, activate store on PATH | — |
| `smith new <name>` | invoke cli-creator to scaffold + self-test a tool | → Tier 0 |
| `smith list` / `smith status` | show tools with version, trust tier, provenance | — |
| `smith add <name>@<ver>` | resolve + verify + install external tool | → Tier 2 (quarantined) |
| `smith run <name> [...]` | run a tool; sandboxed automatically for Tier 0/2 | — |
| `smith promote <name>@<ver>` | **human-gated** Tier 0/2 → Tier 1 (trusted, persisted); review includes reading the source, not just the self-test result | ↑ Tier 1 |
| `smith demote <name>@<ver>` | pull a Tier 1 tool back to quarantine (broken/compromised); removed from PATH immediately, provenance records reason | ↓ Tier 2 |
| `smith publish <name>@<ver>` | sign + push entry to private index (Tier 1 only) | — |
| `smith verify` | re-verify all checksums/signatures/attestations | — |

## 7. Trust gate (HARD REQUIREMENT — all phases)

Trust tiers:
- **Tier 0 — authored, pending review.** Agent-authored. Persists on disk in `store/quarantine/` (otherwise nothing survives to be reviewed in a later session), but is never on the trusted PATH and only executes sandboxed via `smith run`. Sits here until promoted or garbage-collected.
- **Tier 1 — local trusted.** Passed human-gated promotion. Checksum-pinned, persisted, PATH-activated, usable across your machines via the index.
- **Tier 2 — external quarantine.** Pulled from any outside source. Same execution constraints as Tier 0; requires attestation + human review before promotion to Tier 1.

Enforced invariants:
- [ ] Every index entry is `sha256`-pinned; install fails on mismatch.
- [ ] Cosign/SLSA/GitHub-attestation verified when the source offers it; unverifiable external tools are refused unless explicitly quarantined.
- [ ] Tier 0/2 tools **only** execute inside a sandbox profile, and **only** live in `store/quarantine/`, which is never PATH-activated (§5.1). Both halves are required — sandboxing without PATH isolation is bypassable.
- [ ] Promotion (0/2 → 1) and publish are **separate, explicit, human-approved** steps. No command auto-promotes. Promotion review means reading the tool source; a passing self-test is necessary but not sufficient, since it was authored by the same agent that wrote the tool.
- [ ] Trust is revocable: `smith demote` exists from the moment promotion exists, removes the tool from PATH immediately, and records the reason in provenance.
- [ ] Provenance is recorded append-only and hash-chained (each entry commits to the previous entry's hash) per `tool@version`: origin (agent-session-id | human | external URL), created-at, self-test result, reviewer, review timestamp.
- [ ] Manifest/skill text is treated as untrusted data; embedded instructions are never followed.

This is the supervisor-loop kill-test discipline applied to tooling: a tool earns trust *before* it's runnable-unsandboxed, not after something goes wrong.

## 8. Phasing & acceptance criteria

**Phase 0 — env convention + wrapper skeleton**
- [ ] `smith init` activates `store/trusted/` on PATH via mise; `store/quarantine/` is verifiably absent from PATH.
- [ ] `smith add`/`list` work for an mise-backend external tool, landing it at Tier 2; `smith verify` checks sha256 pins (signature verification arrives with the index in Phase 2).
- [ ] A **minimal** `smith promote` exists (records human approval in provenance, moves binary quarantine → trusted) so externally-added tools aren't stuck in quarantine for two phases; full signed-index promotion semantics land in Phase 2.
- [ ] Same `mise.toml` reproduces identical *external* tool versions on two machines (M5 darwin + one Linux box). Authored-tool cross-machine reproduction is a Phase 2 criterion, since it depends on the index + blob transport.

**Phase 1 — cli-creator skill**
- [ ] `smith new` scaffolds a tool meeting all §5.2 agent-friendly criteria.
- [ ] Generated tool passes its own self-test; failure blocks Tier 0 registration.
- [ ] Tool lands at Tier 0 in `store/quarantine/`, persists across sessions, runs only sandboxed, and emits a promotion request. No publish path reachable.

**Phase 2 — signed index + promotion/provenance**
- [ ] `smith promote` (full semantics) moves a Tier 0 tool to Tier 1 only after explicit human approval; provenance recorded; promotion of an authored tool is refused until its blob is pushed to the shared transport.
- [ ] `smith demote` pulls a Tier 1 tool back to quarantine and off PATH immediately.
- [ ] `smith publish` signs and writes the entry; `smith verify` validates signature + checksum.
- [ ] Tier enforcement is provable: a Tier 0/2 tool cannot execute unsandboxed by any command path, including direct PATH invocation.
- [ ] An authored tool promoted on machine A is fetchable, checksum-verified, and runnable on machine B from the index alone.

**Phase 3 — external ingestion hardening (optional, only after 0–2 are solid)**
- [ ] `smith add` of a cosign/SLSA-attested external tool verifies attestation and lands it at Tier 2.
- [ ] Unattested/mismatched external tool is refused or quarantined, never auto-trusted.

## 9. Key risk to hold in view

This system has agents **authoring**, **downloading**, and **running** executable code — and agents are prompt-injectable. A public contribution hub turns that into a malware distribution vector (real precedent: skills that download and run attacker-controlled scripts, and indirect prompt injection hiding instructions inside skill/manifest text). The private-index + curated-allowlist v1 is the deliberate mitigation. Do not open public upload until the trust gate is battle-tested, and even then treat it as a distinct product decision, not a feature toggle.

## 10. Open questions

- **Sandbox mechanism** (blocking Phase 1): bubblewrap vs firejail on Linux; on macOS, `sandbox-exec` is deprecated (still functional but unsupported), so evaluate a Seatbelt profile via that path anyway vs a lightweight container/VM (e.g. `container`/Lima) — cross-platform parity given the M5 + Linux split is the hard part.
- **Blob transport for authored tools** (blocking Phase 2, required by Goal 1): private GitHub Release assets vs git-LFS in the index repo vs a synced content-addressed store. Decides what `smith promote` pushes to.
- **`smith` implementation language**: Go single-binary (matches mise/aqua ecosystem, easy cross-arch distribution) vs Python-uv (faster to iterate, matches your local-model tooling). Leaning Go for the wrapper, allowing python-uv *authored tools*.
- **Versioning scheme for authored tools**: semver (human intent) vs content-hash (reproducibility) vs both (semver tag + content-addressed store). Affects index entry shape.
- **How much to lean on mise's aqua backend** for external tools vs `ubi` directly — resolve during Phase 0.
- **Skill discovery vs tool discovery overlap**: does each authored tool always get a paired `SKILL.md`, or only ones whose trigger conditions are non-obvious? (Discovery-token cost vs findability.)
