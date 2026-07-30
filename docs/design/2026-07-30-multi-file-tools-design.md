# Multi-file tools

Date: 2026-07-30
Status: approved design, pre-implementation

## Problem

A smith tool can only have **one source file**. `sourceFiles()` (`adopt.go:123`)
returns `main.go` for `go`, `<name>.py` + its lock for `python-uv`, and the bare
entrypoint otherwise — plus the three meta files. Everything downstream (vendor
copy, `bundleHash`, `materializeTool`, `upgrade` diffing, two doctor checks,
`verify`) consumes that one list, so a tool physically cannot ship a second
source file.

This is **incidental, not designed**. It falls out of a `switch` and is
documented as a rule nowhere — not in the README, not in SECURITY.md, not in the
cli-creator skill, which only mentions `main.go` in passing.

It has already caused harm. An agent asked to build a supervisor CLI inferred
from the code that "a smith tool is a small, single-file `--json` CLI", promoted
that to a *non-negotiable architecture constraint*, and split its project in two
to route around it. The justification it gave — that multi-file threatens
reviewability — does not survive contact:

- The trust gate is the **adoption PR**, and GitHub reviews multi-file diffs
  natively. That is the normal case for all code review.
- `smith review` is a pre-adoption convenience for reading a tool *before* you
  run `smith use`, not the gate.
- Four 200-line files review better than one 800-line file. File count was never
  what made a diff honest; total size and clarity are.

## Goal / core property

A tool may consist of as many source files as its author needs, in
subdirectories, while every byte that lands in the project stays hashed and
re-verified. Adopting a multi-file tool must be exactly as safe as adopting a
single-file one: nothing on disk in the vendored tree is outside the hash.

Non-goals (deliberately deferred):

- **Third-party dependencies for compiled runtimes.** `buildGoTool` already sets
  `GOPROXY=off`, so a tool needing external modules fails closed. That stays.
  Dependencies at adopt time mean network access, which is the hazard the
  python-uv `--no-build` work closed; deciding it belongs in its own design.
- Changing what `python-uv` vendors (script + lock is its contract).
- Any change to runtime capability. A tool already runs unsandboxed on PATH after
  install and may do anything a shell can; that is unrelated to file count.

## The constraint that shapes the design

**Switching the hash to a tree walk would break every already-adopted tool.**

A scaffolded tool repo contains `README.md`, which is *not* in `sourceFiles` and
therefore is neither vendored nor hashed today. A tree walk would include it, so
every existing pin's `sha256` would change, and `smith install` / `smith verify`
would refuse every tool already adopted anywhere. That is unacceptable.

So multi-file is **opt-in per tool, declared in its manifest**. A tool that says
nothing keeps the legacy file list and a byte-identical hash, forever.

## Design

### 1. `sources:` in the manifest

```yaml
name: mytool
runtime: go
entrypoint: mytool
sources: [main.go, internal/, templates/]
```

- **Absent** → legacy behavior. `sourceFiles()` unchanged, hash unchanged.
- **Present** → the tool's source is exactly these entries. A directory entry is
  walked recursively. The three meta files (`manifest.yaml`, `selftest.sh`,
  `SKILL.md`) are always included, as today.

Explicit entries rather than "walk the whole repo" for three reasons: it cannot
silently vendor junk (`.DS_Store`, `node_modules`, a stray 40MB fixture); it
gives the reviewer a one-line statement of what the tool *is*; and it keeps the
author in control of the review surface they're asking for.

### 2. Resolution rules (all fail closed)

`resolveSources` expands `sources:` into a sorted list of relative file paths:

1. **Reject absolute paths and any `..` segment.** A `sources:` entry is a path
   inside the tool repo, nothing else.
2. **Reject symlinks anywhere** — as an entry or encountered during a directory
   walk. A symlink inside a tool repo can point outside it, and the vendor copy
   would follow it, pulling arbitrary host files into the project. `Lstat` every
   path.
3. **A listed entry that does not exist is an error**, not a skip. The current
   `os.IsNotExist → continue` tolerance is right for a fixed list smith
   generates; it is wrong for a list the author wrote, where a typo would
   silently ship a tool missing a file.
4. **Deterministic order.** Collect, then `sort.Strings`. The hash format
   (`sorted "<rel>\0<sha>\n"`) is unchanged — only the file *set* differs — so
   `bundleHash` needs no modification at all.
5. **Refuse an empty result.** A tool with no source is a packaging mistake.
6. **Caps.** At most 200 files and 2 MiB of source total, with an error naming
   what exceeded which limit. Not arbitrary bureaucracy: the review surface is
   the trust gate, and a tool nobody can read is a tool nobody can vouch for.
   Generous enough that no honest tool hits it; tight enough that a vendored
   `node_modules` fails loudly instead of landing in someone's repo.

### 3. Vendor copy walks subdirectories

Today's copy loop (`adopt.go:896`) writes each file flat into `.smith/tools/<n>/`.
With nested sources it must `MkdirAll` each file's parent. Execute bits stay as
they are: `0755` for the entrypoint and `selftest.sh`, `0644` otherwise — a
vendored source file has no business being executable.

### 4. Go builds a package

`buildGoTool` runs `go build -trimpath -o <out> main.go`. With `sources:` it
builds the **package** (`go build -trimpath -o <out> .`) so sibling files
compile. `GOPROXY=off`, `CGO_ENABLED=0` and the offline posture are unchanged, so
a tool that reaches for a third-party module still fails closed — deliberately,
per non-goals.

### 5. Everything else follows for free

`sourceFiles()` is called from 7 places (adopt, install, upgrade, two doctor
checks, verify). Giving it the manifest's `sources` and returning the resolved
list means hashing, materializing, diffing, verifying and doctor all update
together. That is the whole point of it being one seam.

One wrinkle: at *install* time there is no clone to read a manifest from — only
the vendored tree and the lockfile. So the resolved list must be recoverable
from the vendored copy. The vendored tree contains `manifest.yaml`, so install
reads `sources:` from the vendored manifest, which is itself hashed — a tampered
`sources:` changes the bundle hash and `install` refuses. No new lockfile field
is needed, and the integrity story is unchanged.

## Testing

- `resolveSources`: rejects `..`, absolute paths, and symlinks (entry *and*
  nested); errors on a missing listed entry; errors on empty; enforces both caps;
  returns sorted output; includes meta files.
- **Legacy hash is byte-identical.** A tool with no `sources:` hashes exactly as
  before — the regression that would break every adopted tool. Pin it with a
  literal expected digest, not a recomputation.
- Adopt → install → run a genuinely multi-file Go tool (two files, second file
  supplying a function the entrypoint calls) end to end.
- Nested subdirectory survives the vendor copy with its structure and modes.
- Tampering with a nested vendored file makes `install`/`verify` refuse.
- Tampering with the vendored `manifest.yaml`'s `sources:` list is caught by the
  hash.
- A `sources:` Go tool needing a third-party module still fails closed.

## Docs to update

`smith contract` (the single-file limit stops being true), the cli-creator skill
(Runtimes + shape sections), README, and `CLAUDE.md`'s "change X → also update Y"
table — adding a runtime already has an entry; `sources:` resolution should be
named beside it as the other thing that decides a tool's file set.
