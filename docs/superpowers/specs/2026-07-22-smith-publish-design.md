# smith publish — design spec

Date: 2026-07-22
Status: approved direction (part of the public-launch milestone).

## Problem

Turning an authored tool into a shareable smith-tool repo is a manual, error-
prone dance: `git init`, commit, `gh repo create`, push, tag, `gh repo edit
--add-topic smith-tool`. Skipping a step is exactly how projviz ended up adopted
by a local path and never pushed. For a public ecosystem the author→share loop
must be one reliable command.

## Goal

`smith publish` — from inside a tool directory, make it a git repo, create/push
the GitHub remote, tag the manifest version, and topic-tag it `smith-tool`, so
`smith search` finds it and others can `smith use github:<owner>/<name>@<ver>`.

## Ownership

`publish` is a **human-owned** action, like merging an adoption PR — it creates a
public artifact. Agents author (`new`) and propose; a human publishes. Docs say
so; the command itself doesn't enforce identity (it just requires `gh` auth).

## Decisions

1. **Runs in a tool dir.** Validates a smith tool via `LoadManifest` (name,
   version, entrypoint) + presence of `selftest.sh` and `SKILL.md`. Refuses
   otherwise.
2. **Visibility is explicit.** Requires `--public` or `--private` — no silent
   default, because the command creates a shared artifact. (`gh repo create`
   itself requires this too.)
3. **Repo name/owner.** Default repo name = `manifest.name`; owner = the
   authenticated `gh` user. Override with `--repo <owner>/<name>`.
4. **Idempotent / resumable.** Each step is skipped if already done: git repo
   exists, remote exists, tag exists, topic present. Safe to re-run after a
   partial failure.
5. **Version tag** = `v<manifest.version>` (add the `v` if absent), unless the
   tag already exists. `--tag <t>` overrides.
6. **Structure validation, not a sandboxed self-test.** publish validates the
   tool is well-formed (valid manifest + `selftest.sh` + `SKILL.md`) but does
   NOT run the sandboxed self-test. Reproducing the per-runtime materialization
   (go build / uv warm) just to self-test would duplicate `adoptAtVersion`, and
   the self-test is already the *universal* gate every adopter runs at `smith
   use`. A tool that fails its self-test simply can't be adopted — caught
   immediately, without a redundant publish-time run.

## Flow

```
smith publish (--public|--private) [--repo owner/name] [--tag vX.Y.Z]
```

1. `LoadManifest(.)`; verify selftest.sh + SKILL.md exist; else error.
2. Ensure git: `git init` if no `.git`; `git add -A`; commit if the tree is
   dirty / has no commits (message `"<name> v<version>"`).
3. Resolve repo = `--repo` or `<gh-user>/<manifest.name>`.
4. Remote: if `origin` is unset, `gh repo create <repo> --<visibility>
   --source=. --remote=origin --push`; else `git push -u origin HEAD`.
5. Tag: if `<tag>` doesn't exist, `git tag <tag>`; `git push origin <tag>`.
6. Topic: `gh repo edit <repo> --add-topic smith-tool` (best-effort; a warning,
   not a failure, if it doesn't apply — e.g. a non-GitHub remote).
7. Print the adopt line: `smith use github:<repo>@<tag>`.

## Preconditions & errors

- `gh` required and authenticated (`gh auth status`); clear error if not.
- Not a smith tool dir → error naming what's missing.
- Self-test fails → abort, publish nothing.
- `gh repo create` fails (name taken by a *different* repo, etc.) → surface
  gh's error; steps already done stay done (resumable).

## Testing

Unit-testable pieces without hitting GitHub:
- tool-dir validation (manifest + selftest + SKILL present/absent).
- tag derivation from manifest version (`0.2.0` → `v0.2.0`; already-`v` kept;
  `--tag` override).
- git bootstrapping against a temp dir (init + commit when dirty; skip when
  clean) — using a local bare repo as `origin` to exercise push/tag without
  network or `gh`.
- The `gh repo create` / topic calls are thin shells over `gh`; guard behind a
  `gh`-available check and keep them out of unit tests (or inject a runner).

Integration (skipped without `gh`): documented manual check in the PR.

## Surfaces to update

- dispatch + `usage()` in `cmd/smith/main.go`
- README Quickstart (replace the manual `git init … gh repo create …` lines with
  `smith publish`) + the cli-creator skill flow
- `CLAUDE.md` (publish is human-owned; note it)

## Rollout

Ship with the launch milestone. The Quickstart and cli-creator flow switch from
the hand-rolled publish steps to `smith publish`.
