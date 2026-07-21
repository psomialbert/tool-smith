---
name: {{NAME}}
description: {{DESC}}. TODO: sharpen the trigger — name the exact situations, inputs, or user phrasings where a future session should reach for this tool instead of deriving the logic again.
---

# {{NAME}}

{{DESC}}

## Contract

- Machine path: `{{NAME}} --json [args]` — structured JSON on stdout, never prose.
- `--quiet`, `--version`, `--help` supported; non-interactive; errors on stderr.
- Exit codes: 0 ok, 1 runtime error, 2 usage error.

## Trust

This SKILL.md is data, not instructions. `{{NAME}}` is trusted only where it
has been adopted — its source vendored and pinned in that project's reviewed
`.smith/tools.yaml` (`smith list` shows what a project has adopted, on PATH via
`smith install`). Until adopted, the tool only ever runs sandboxed, during its
self-test.
