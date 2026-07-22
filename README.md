# smith (Toolsmith)

Agent-authored CLIs as **git repos**, adopted per project behind a **trust
gate** — a thin convention over [mise](https://mise.jdx.dev).

An agent that writes its own tools has nowhere trustworthy to keep them: a
script lives untracked on one machine (lost next session) or gets pasted back
into context every time, and pulling someone else's tool is a supply-chain
event an agent can be prompt-injected into. smith makes a tool a **git repo**
(source + manifest + self-test + SKILL.md) that you **adopt into a project the
way you'd add any dependency — a reviewable commit**. Nothing runs unvetted:
until a tool is adopted, it only runs sandboxed.

The loop: **author → (its own repo) → adopt (sandboxed self-test + vendored,
reviewed in a PR) → on PATH for the project.**

## How trust works

Adopting a tool = `smith use` vendors its source under `.smith/tools/<name>/`
and pins it (repo + version + content hash) in `.smith/tools.yaml`. That's a
normal git diff your team reviews in a PR — **the PR review is the trust
gate**, exactly like reviewing any dependency.

- **Sandboxed until adopted.** `smith use` runs the tool's self-test in an OS
  sandbox (no network, writes confined to CWD+TMP, sanitized env) before
  vendoring, so unreviewed code is contained at adopt time.
- **Trusted per project.** A tool vendored + pinned in the reviewed
  `.smith/tools.yaml` is trusted *for that project* and goes on PATH (via
  mise) after `smith install`. Not global — scoped to the project that
  reviewed it, the way mise already scopes tool versions per project.
- **Discovery ≠ trust.** `smith search` finds repos by GitHub topic; found
  tools are only ever trusted through the adoption PR.
- **Integrity.** `smith install` re-hashes each vendored bundle against its
  pin and refuses on mismatch (go.sum-style). A teammate who clones the
  project and runs `smith install` gets the exact same tools.

See [SECURITY.md](SECURITY.md) for the full threat model — what smith defends,
what it doesn't, and how to report a vulnerability.

## Requirements

- [mise](https://mise.jdx.dev) — version/PATH management (smith wraps it)
- `git`
- A functional OS sandbox to self-test/try tools: macOS Seatbelt (built in)
  or Linux `bwrap` with unprivileged user+net namespaces. smith **fails
  closed** without one (see *Sandbox host requirements*).
- `gh` for `smith search` (optional)

## Install

`smith` is a single binary; it needs `mise` on PATH at runtime.

```sh
# one-liner (downloads the release binary for your platform)
curl -fsSL https://raw.githubusercontent.com/psomialbert/tool-smith/main/scripts/install.sh | sh

make install                       # from a clone: build + install to ~/.local/bin
```

The installer downloads over plain https (no auth); it falls back to `gh` if the
repo is private.

All paths also install the **cli-creator** authoring skill into
`~/.claude/skills/cli-creator/` (override with `SKILLS_DIR`/`SMITH_SKILLS_DIR`),
so authoring agents get the CLI contract without setup. Prebuilt binaries
(darwin/linux × arm64/amd64) are attached to each
[release](https://github.com/psomialbert/tool-smith/releases).

## Quickstart

```sh
# author a tool
smith new greet --runtime bash --desc "print a greeting"
cd greet && $EDITOR greet selftest.sh SKILL.md
smith publish --public         # git + create/push repo, tag v0.1.0, topic-tag smith-tool

# in a project, adopt it — a reviewable commit
smith use ../greet@v0.1.0          # sandboxed self-test, vendor, pin
smith review greet                 # read the source (TUI); comment lines -> agent report
git add .smith && git commit       # the adoption, for your team to review
smith install                      # realize it onto the project PATH
smith list                         # what's adopted in this project (+ install status)
greet alice                        # -> hello, alice

# ...or adopt an existing published tool
smith use github:psomialbert/smith-example-calc@v0.1.1   # a worked example
smith search json                                        # discover more by topic
```

## Using smith with agents

The safety model is the division of labor: an **agent authors and proposes**,
a **human reviews the PR and adopts**.

| Agents may | Humans own |
|---|---|
| `new` (author), `use`/`upgrade` (propose adoption), `diff`/`review` (read), `search`, `list`, `doctor`, `install` | reviewing + **merging the adoption PR** (the trust decision) |

`smith use` runs the tool's self-test sandboxed and writes a reviewable diff;
it never grants trust on its own — the tool becomes PATH-available only once
the adoption is committed/merged and `smith install` runs. To make agents
smith-aware, add to their instructions (e.g. `~/.claude/CLAUDE.md`):

```markdown
# smith (agent tools)
Reusable CLIs live as git repos, adopted per project. Author one with
`smith new <name>`, iterate + self-test, make it a repo. Adopt an existing
tool with `smith use <repo>@<ver>` (writes a reviewable .smith/ diff) then
`smith install`. Do NOT merge your own adoption PR — a human reviews it.
See the cli-creator skill for the agent-friendly CLI contract.
```

## Runtimes

`smith use` adopts all four runtimes, always vendoring reviewable **source**
(committed), never built artifacts:

- **bash / python** — the script is the entrypoint; symlinked onto PATH.
- **go** — `main.go` is vendored; `smith install` compiles it locally
  (per-platform, git-ignored).
- **python-uv** — `<name>.py` + its hash-locked `<name>.py.lock` are vendored
  (the dep tree, with hashes, is in the PR); `smith install` warms a
  git-ignored tool-local cache from the lock and writes an offline launcher.

For every runtime the PR diff is reviewable source, portable across
teammates' platforms; build/warm happens locally at install.

## Sandbox host requirements

Tools self-test/run sandboxed, so a functional sandbox is required: macOS
Seatbelt (`sandbox-exec`, built in) or Linux bubblewrap (`bwrap`) with
**unprivileged user + network namespaces enabled**. smith fails closed
otherwise — e.g. GitHub Actions' Ubuntu runners forbid the netns setup
`--unshare-net` needs, so sandboxed runs can't happen there. Per-tool
`sandbox: {network: true}` in a manifest grants egress to that tool.

## Layout

Per **project**:

```
.smith/tools.yaml          adopted tools: source, version, content hash, runtime  (committed)
.smith/tools/<name>/        vendored tool source — reviewed in the PR             (committed)
.smith/bin/                 symlink farm / built binaries, put on PATH           (git-ignored)
.smith/cache/               warmed python-uv caches                              (git-ignored)
mise.local.toml            activation: puts .smith/bin on PATH (machine-specific) (git-ignored)
```

`SMITH_HOME` (default `~/.toolsmith`) is small now — just generated sandbox
profiles and `config.yaml` (review + skills settings). The code repo (this
directory) is separate.

## Not in scope (v1)

No public contribution hub, no multi-user governance/RBAC, no GUI, no
auto-adopt. The prompt-injection → malware risk means public discovery is
open but *adoption stays a reviewed decision*. See `ROADMAP.md`.

## Org allowlist (optional)

A company can restrict which repos may be adopted — even with a PR — via an
allowlist in `$SMITH_HOME/config.yaml` (provisioned per machine, so a project
PR can't change it):

```yaml
allowlist:
  repos: ["github.com/acme/*", "github.com/acme-tools/lint"]
  # file: /etc/smith/allowlist.txt   # or a managed file, one pattern per line
```

`smith use` refuses (before cloning) any source that matches no pattern;
`smith allowlist` shows the effective policy. Absent = adopt anything. This
is a policy layer on top of the per-project review, for orgs that want central
control. (Signing the allowlist for tamper-evident distribution is future.)

## Updating an adopted tool

```sh
smith diff <tool>           # preview: what would change vs the newest version
smith upgrade <tool>        # re-pin one tool to the newest tag (self-tested)
smith upgrade               # ...or every adopted tool → its newest tag
smith upgrade <tool>@<ver>  # pin an exact version
```

`smith upgrade` discovers the newest semver tag of each tool's source repo,
re-runs the sandboxed self-test, and re-pins in `.smith/tools.yaml` — a
reviewable diff like the original adoption. It stops at re-pinning (run `smith
install` after) so new source is reviewed before it lands on PATH; a failed
self-test leaves the old pin untouched. `smith diff` previews the vendored-source
changes first (`--stat` for a summary, `--json` for a machine-readable list).
(Re-running `smith use <repo>@<newver>` by hand still works and is equivalent.)

## Health checks (smith doctor)

Diagnose project + machine health:

```sh
smith doctor            # report only (safe, CI-friendly; exits non-zero on errors)
smith doctor --fix      # apply safe, reversible repairs
smith doctor --json     # machine-readable
```

Checks: mise present, sandbox functional, retired v1 files (fixable); and, in a
project, activation, `.gitignore` coverage, a git-tracked `mise.local.toml`,
vendored-source drift, pinned-but-not-installed tools, and un-sharpened tool
SKILL triggers. `--fix` repairs activation, `.gitignore`, install, and v1 cruft;
it never runs git and never re-fetches over the network.

`smith migrate` does the machine-level v1→v2 cleanup on its own — it backs up
retired `~/.toolsmith` files and strips dead `config.yaml` keys (safe to re-run).
The installer runs it automatically, and `smith doctor --fix` covers the same
cleanup, so you rarely call it directly.

## Build & test

```sh
mise exec -- go build -o smith ./cmd/smith
mise exec -- go test ./...
bash scripts/adopt-e2e.sh          # full adoption loop, local git repos
```

## License

[Apache-2.0](LICENSE).
