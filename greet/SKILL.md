---
name: greet
description: Print a friendly greeting as JSON or plain text. Use when a session needs a canned hello line for a named recipient (demo, smoke-test, or example tool).
---

# greet

print a greeting

## When to use

A minimal worked example of a smith tool. Reach for it when you need a trivial,
well-behaved CLI to demonstrate or smoke-test the adopt → install flow, or as a
copy-from starting point when authoring your own tool.

## Contract

- Machine path: `greet --json [args]` — structured JSON on stdout, never prose.
- `--quiet`, `--version`, `--help` supported; non-interactive; errors on stderr.
- Exit codes: 0 ok, 1 runtime error, 2 usage error.

## Usage

```sh
greet --json alice
```
