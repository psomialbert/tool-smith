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
make install                       # from a clone: build + install to ~/.local/bin
sh scripts/install.sh              # any machine with gh: download the release binary
```

All paths also install the **cli-creator** authoring skill into
`~/.claude/skills/cli-creator/` (override with `SKILLS_DIR`/`SMITH_SKILLS_DIR`),
so authoring agents get the CLI contract without setup. Prebuilt binaries
(darwin/linux × arm64/amd64) are attached to each
[release](https://github.com/psomialbert/tool-smith/releases).

## Quickstart

```sh
# author a tool, make it a repo
smith new greet --runtime bash --desc "print a greeting"
cd greet && $EDITOR greet selftest.sh SKILL.md
git init && git add -A && git commit -m "greet v0.1.0" && git tag v0.1.0
# (push it: own repo, org repo, or embed in a project; topic-tag it smith-tool)

# in a project, adopt it — a reviewable commit
smith use ../greet@v0.1.0          # sandboxed self-test, vendor, pin
smith review greet                 # read the source (TUI); comment lines -> agent report
git add .smith && git commit       # the adoption, for your team to review
smith install                      # realize it onto the project PATH
greet alice                        # -> hello, alice

# discover others
smith search json
```

## Using smith with agents

The safety model is the division of labor: an **agent authors and proposes**,
a **human reviews the PR and adopts**.

| Agents may | Humans own |
|---|---|
| `new` (author), `use` (propose adoption), `review` (read), `search`, `list`, `install` | reviewing + **merging the adoption PR** (the trust decision) |

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

`smith new` scaffolds `bash`, `python` (stdlib), `go` (compiled), and
`python-uv` (PEP 723 + hash-locked deps). `smith use` adopts `bash`, `python`,
and `go`. For **go**, the reviewable **source** (`main.go`) is vendored and
committed — never a binary — and `smith install` builds it locally per
platform (git-ignored), so the PR diff stays reviewable and portable across
teammates' platforms. (`python-uv` adoption — lock + warm at install — is the
next runtime.)

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
.smith/bin/                 symlink farm put on PATH via mise.local.toml          (git-ignored)
```

`SMITH_HOME` (default `~/.toolsmith`) is small now — just generated sandbox
profiles and `config.yaml` (review + skills settings). The code repo (this
directory) is separate.

## Not in scope (v1)

No public contribution hub, no multi-user governance/RBAC, no GUI, no
auto-adopt. The prompt-injection → malware risk means public discovery is
open but *adoption stays a reviewed decision*. A signed **allowlist** (which
repos an org permits adopting) is a planned optional policy layer. See
`ROADMAP.md`.

## Build & test

```sh
mise exec -- go build -o smith ./cmd/smith
mise exec -- go test ./...
bash scripts/adopt-e2e.sh          # full adoption loop, local git repos
```
