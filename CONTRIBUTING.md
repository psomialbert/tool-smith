# Contributing to smith

Thanks for looking. This is a small, security-sensitive project — a tool whose
whole job is deciding what code your agent is allowed to run — so the bar for
changes is "would I want this deciding that on my machine".

## Getting set up

```sh
git clone https://github.com/psomialbert/tool-smith && cd tool-smith
make build          # builds ./smith
make test           # the full suite
```

Requirements: [mise](https://mise.jdx.dev) (Go is pinned via `mise.toml`, so run
Go through `mise exec -- go ...` or just use `make`), `git`, and a functional OS
sandbox — macOS Seatbelt is built in; Linux needs `bwrap` with unprivileged
user+net namespaces. Some tests skip without a working sandbox rather than
passing vacuously; `smith doctor` will tell you what's missing.

## Before you open a PR

```sh
make test
mise exec -- go vet ./...
mise exec -- gofmt -l .        # must print NOTHING (CI enforces this)
bash scripts/adopt-e2e.sh      # end-to-end, local git repos only, no network
```

`main` is protected — land changes through a PR with CI green.

## Things that will come up in review

**The command surface is described in several places that drift silently.** If
you add, remove, or rename a command, update the dispatch `switch` *and*
`usage()` in `cmd/smith/main.go`, the README, and `skills/cli-creator/SKILL.md`
if it's part of the author or adopt flow. `CLAUDE.md` has the full checklist and
a "change X → also update Y" table; it exists because each entry has bitten us
at least once.

**Two invariants fail silently if you miss them**, so they're worth naming here:

- Adding a top-level key to `config.FileConfig` requires adding it to
  `v2ConfigKeys` in `cmd/smith/migrate.go`. That map is an allowlist — a live key
  missing from it gets *stripped* from users' configs as "retired".
- Adding a sandbox capability means enforcing it in **both** the macOS seatbelt
  profile and the Linux bwrap args, and including it in the profile cache key.
  A grant that's declared but not enforced is worse than no grant.

**Tests should fail for the right reason.** A test that passes against the
unfixed code isn't a test. If you're fixing a bug, the most useful thing you can
put in the PR is evidence the new test fails without your fix.

**Security-relevant changes want a threat sentence.** Not a long analysis — just
what an attacker controls, and what stops them. If a change touches the trust
gate, the sandbox, or anything that reaches an agent's instruction files, say so
explicitly; see [SECURITY.md](SECURITY.md) for the model those changes live in.

## Reporting a vulnerability

Please don't open a public issue. Use GitHub's **"Report a vulnerability"** on
this repo — details and scope in [SECURITY.md](SECURITY.md).

## Two commands are deliberately human-owned

`smith publish` and merging an adoption PR both create or authorize shared
artifacts, so agents propose them and humans complete them. That asymmetry is
the design, not an oversight — please don't "fix" it by making either run
automatically as a side effect of something else.

## Scope

Ideas beyond the current surface live in
`docs/superpowers/specs/2026-07-21-smith-feature-landscape.md`. If you're
planning something substantial, open an issue or a discussion first — it's
cheaper than finding out in review that the design conflicts with the trust
model.
