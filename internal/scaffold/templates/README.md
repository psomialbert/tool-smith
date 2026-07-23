# {{NAME}}

{{DESC}}

A [smith](https://github.com/psomialbert/tool-smith) tool — an agent-friendly
CLI adopted into a project behind a trust gate.

## Adopt it

```sh
smith use <owner>/{{NAME}}@v{{VERSION}}  # sandboxed self-test, vendored, pinned
smith install                            # onto the project's PATH
{{NAME}} --help
```

Adopting vendors this tool's source into your project (`.smith/tools/{{NAME}}/`)
and pins it — a reviewable commit, like any dependency. Until adopted, it only
runs sandboxed.

## Contract

Agent-friendly: `--json` for structured output, `--quiet`, `--version`,
`--help`; non-interactive; errors on stderr; exit codes 0 ok / 1 runtime / 2
usage.

```sh
{{NAME}} --json [args]
```

## Develop

Edit the entrypoint and `selftest.sh`, then tag a release:

```sh
git commit -am "..." && git tag v<next>
```

`smith use` runs `selftest.sh` (sandboxed) before adopting, so keep it
meaningful — it's the gate that a new version must pass.
