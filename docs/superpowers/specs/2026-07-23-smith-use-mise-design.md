# smith use --mise — external CLI adoption

Date: 2026-07-23
Status: approved direction.

## Problem

Teams want to standardize a *third-party* CLI (e.g. `pup`, `ripgrep`, `jq`)
across a project the same reviewable way smith adopts its own tools — the
original "make all devs use pup" question. smith tools are git repos whose
source is vendored + reviewed; a third-party CLI has no source to vendor. mise
already fetches/pins such tools; smith should offer a first-class, reviewable
per-project adoption on top of it.

## Decisions

1. **Representation:** `smith use --mise <spec>[@<ver>]` writes the tool+version
   to the project's committed `mise.toml` (the standard place mise expects it)
   *and* records a `runtime: mise` entry in `.smith/tools.yaml` so `smith list`
   shows it. The review surface is the `mise.toml` diff.
2. **Trust posture:** the pin is a reviewable commit like any dependency bump.
   **No sandboxed self-test** (it's not a smith tool), **no SKILL export**.
   `smith list` and the commit label it external/unvendored so it's never
   mistaken for a source-reviewed smith tool. The **allowlist still applies**.
3. **Honest limit:** smith does not review the third-party CLI's source — you
   trust the upstream tool and mise's fetch, exactly as you would using mise
   directly. smith adds: one command, a per-project pin, and a unified `list`.

## Flow

`smith use --mise ubi:DataDog/pup@1.2.3` (or `--mise node@20`, `--mise
go:github.com/x/y`):

1. Parse `spec` + `version` (trailing `@ver`).
2. Allowlist check on the spec (and its repo part) — refuse if it matches none.
3. If no version: `mise latest <spec>` → a concrete version (reproducible pin).
4. `mise use <spec>@<version>` — mise writes `mise.toml` and installs locally.
5. Record `.smith/tools.yaml`: `{source: "mise:<spec>", version, runtime: mise}`
   (no sha256, no entrypoint).
6. Print the reviewable-commit guidance; teammates run `smith install`.

## Behavior of other commands on a `runtime: mise` entry

- **`smith install`** — skip vendored-sha/materialize; instead run `mise install`
  once (installs everything in `mise.toml`). No SKILL export.
- **`smith verify`** — report `external (mise) — not vendored`, never a
  hash failure (nothing to hash). Not counted as a problem.
- **`smith list`** — shown with runtime `mise`; status reflects mise, not
  `.smith/bin`.
- **`smith doctor`** — the `vendored-sha` and `installed` checks skip mise
  entries (they have no vendored source and no `.smith/bin` symlink).

A `ProjTool.isMise()` helper (`Runtime == "mise"`) guards each spot.

## Testing

- spec/version parse (`ubi:DataDog/pup@1.2.3`, backend colons preserved).
- allowlist refusal for a non-permitted spec.
- record round-trip: a mise entry saved to `.smith/tools.yaml` loads back.
- install/verify/list/doctor **skip** a mise entry (no sha attempted, no
  materialize, verify reports external, doctor doesn't flag it).
- The `mise use`/`mise latest`/`mise install` shell-outs are guarded behind a
  `mise`-available check and exercised in a documented manual/integration check
  (kept out of unit tests to avoid network).

## Surfaces to update

- `cmdUse` (--mise routing) + `usage()` + README (a "Third-party CLIs" section)
- `CLAUDE.md` (the isMise guard points; external ≠ vendored trust)
- cli-creator is unchanged (it's about authoring smith tools, not external CLIs)

## Rollout

Ships as a minor (v2.10.0). Additive; no breaking changes.
