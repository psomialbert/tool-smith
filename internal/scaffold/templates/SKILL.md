---
name: {{NAME}}
description: {{DESC}}. TODO: sharpen the trigger — name the exact situations, inputs, or user phrasings where a future session should reach for this tool instead of deriving the logic again.
---

# {{NAME}}

{{DESC}}

## When to use

TODO: the concrete situations, inputs, and user phrasings this tool is for —
so a future session reaches for it instead of re-deriving the logic.

## Contract

- Machine path: `{{NAME}} --json [args]` — structured JSON on stdout, never prose.
- `--quiet`, `--version`, `--help` supported; non-interactive; errors on stderr.
- Exit codes: 0 ok, 1 runtime error, 2 usage error.

## Usage

```sh
{{NAME}} --json [args]
```
