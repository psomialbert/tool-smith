# smith — working notes for Claude

smith: agent tools as git repos, adopted per project behind a trust gate.
The workflow is **`new → use → install`**. The v1 model (personal signed index,
`register`/`run`/`promote`, Tier 0/quarantine, `$SMITH_HOME/drafts/`) is
**retired** — never reintroduce those verbs in code or docs.

## Before every commit / push: sync check

The command surface and flow are described in several places that drift out of
sync silently. **Before committing, verify each still matches actual behavior:**

- [ ] **`skills/cli-creator/SKILL.md`** — the agent-loaded authoring skill. Must
      teach the real verbs and flow. This has bitten us: a stale skill makes
      agents run retired commands.
- [ ] **`README.md`** — user-facing usage, command list, workflow.
- [ ] **`usage()` in `cmd/smith/main.go`** — the `smith help` text. Keep the
      command list identical to the dispatch `switch` above it.
- [ ] **`cmdNew` printed "next steps"** (`cmd/smith/main.go`) — the steps a tool
      author sees after `smith new`. These are the instructions people actually
      follow; keep them correct.
- [ ] **`scripts/install.sh`** — the "next:" hint and any referenced commands
      (it once pointed at the removed `smith init`).
- [ ] **`internal/scaffold/templates/README.md` and `SKILL.md`** — inherited by
      every new tool repo; they reference `smith use` + the contract.

## Change X → also update Y

- **Add / remove / rename a command** → dispatch `switch` *and* `usage()` in
  `main.go`, `README.md`, and the cli-creator skill if it's part of the author
  or adopt flow.
- **Add / rename a `config.FileConfig` key** (`internal/config/config.go`) →
  add it to `v2ConfigKeys` in `cmd/smith/migrate.go`. That map is an allowlist;
  a live key missing from it gets **stripped as "retired"** by `smith migrate`.
- **Add a runtime** (`internal/scaffold/Runtimes`) → scaffold template
  `tool.<runtime>`, `SourceFile`, `sourceFiles`/materialize switch in
  `adopt.go`, the `--runtime` help in `cmdNew` + `usage()`, and the skill's
  Runtimes section.
- **Change the adoption/trust flow** → cli-creator skill, README, and the tool
  template README all describe it; update together.
- **Add a health check** → add it to the `doctorChecks()` registry in
  `cmd/smith/doctor.go` as a diagnose/apply pair. Reuse the shared halves
  (`activationState`, `gitignoreMissing`, `materializeTool`, `diagnoseV1Home`,
  `bundleHash`, `sandbox.Functional`) — don't duplicate detection. `--fix` runs
  only fixable (safe/reversible) checks and never runs git.

## smith doctor

The proactive health command: `smith doctor` reports machine + project issues,
`--fix` repairs the safe/reversible ones. It consolidates logic that used to
fire only reactively (during install/use/migrate). When you add repair/verify
behavior anywhere, expose it as a doctor check too, so it's discoverable without
tripping over it.

## Releasing (this bit us — read it)

`scripts/install.sh` downloads **`cli-creator-SKILL.md` as a release asset**, so
a skill fix only reaches fresh installs once it's in a **new release**. After
merging a change that touches the skill or the binary:

1. Tag `v<x.y.z>` on `main`, push the tag.
2. `make cross` (builds the 4 binaries + copies `cli-creator-SKILL.md` into `dist/`).
3. `gh release create v<x.y.z> ... dist/smith-* dist/cli-creator-SKILL.md`.

Historical `register`/`promote` mentions in `ROADMAP.md` and `toolsmith-spec.md`
are accurate history/original-design docs — leave them; they are not
agent-loaded.

## Repo conventions

- `main` is protected: land changes via a PR (`gh pr create` → CI green →
  merge). Direct pushes of tags are fine.
- Commit trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- Go is pinned via `mise.toml`; run Go through `mise exec -- go` (or `make`).
- Run `make test` + `mise exec -- go vet ./...` before pushing.
