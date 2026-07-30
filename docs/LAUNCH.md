# Going public — launch checklist

smith is feature-complete and released. The code side is done; the steps below
are the human go-live actions, in order. Several GitHub features only become
available *after* the repo is public, so order matters.

**Status as of 2026-07-30:** steps 1, 2, 3 and 5 are **done** — both repos are
public, private vulnerability reporting is enabled, Dependabot alerts + security
updates are on, and the anonymous install path is verified end to end (fetch
`install.sh`, download assets, verify `SHA256SUMS`, clone, `use` → `install` →
invoke → `verify` → `lens`, all with no credentials). Remaining: the two optional
polish items, **4** (GitHub Sponsors) and **6** (repo topics — currently empty;
the description is set). Kept below as the record of what was done and why.

## 1. Flip the repos public

```sh
gh repo edit psomialbert/tool-smith --visibility public --accept-visibility-change-consequences
gh repo edit psomialbert/smith-example-calc --visibility public --accept-visibility-change-consequences
```

`smith-example-calc` must be public too — the README Quickstart adopts it
(`smith use github:psomialbert/smith-example-calc@v0.1.1`), so a newcomer's
first run needs it reachable.

## 2. Enable Private vulnerability reporting

Only appears once the repo is public: **Settings → Security & analysis →
Private vulnerability reporting → Enable**. `SECURITY.md` directs reporters to
this "Report a vulnerability" flow.

## 3. Enable the dependency graph / Dependabot

Automatic on public repos; otherwise **Settings → Security & analysis →
Dependency graph** (and Dependabot alerts/updates). This activates the weekly
update PRs from `.github/dependabot.yml` (gomod + github-actions).

## 4. (optional) Enable GitHub Sponsors

`.github/FUNDING.yml` points at `github: psomialbert`; the Sponsor button only
renders once GitHub Sponsors is enabled for the account. Edit that file for
other platforms, or remove it.

## 5. Verify the public install path

From a machine **without** `gh` auth (proves the anonymous path):

```sh
curl -fsSL https://raw.githubusercontent.com/psomialbert/tool-smith/main/scripts/install.sh | sh
smith version
```

The installer downloads release assets over plain https once the repo is public
(it falls back to `gh` only while private).

## 6. (optional) Polish

- Repo description + topics on `tool-smith` for discoverability.
- Announce; point people at the README + `SECURITY.md` threat model.

---

## What's shipped

Released through **v2.14.0**. Command surface: `new · publish · search · use ·
use --mise · review · install · upgrade · diff · verify · lens · list ·
allowlist (keygen/sign/verify) · migrate · doctor` — unchanged since v2.13.x;
multi-agent discovery added no new commands.

**Multi-agent discovery** (v2.14.0) means smith is no longer Claude-specific:
`smith install` emits the adopted-tool index through an `agentTarget` registry —
the machine-global Claude skills export plus a managed block in whichever of
`AGENTS.md` / `CLAUDE.md` already exists. Selected by the `agents.targets` config
key; reported by the `agents-block` doctor check.

Remaining ideas live in `docs/superpowers/specs/2026-07-21-smith-feature-landscape.md`
(watch-list: sandbox v2, tool composition, org catalog, monorepo, usage-aware
suggestions, execution receipts) — none required for launch.
