package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"smith/internal/config"
	"smith/internal/scaffold"
)

// cmdContract prints the tool-authoring contract: what a smith tool must be,
// what the adopt-time sandbox does and does not constrain, and which limits are
// load-bearing versus merely unbuilt.
//
// Why this is a command and not only a skill: the cli-creator skill is installed
// out-of-band (a release asset landing in an agent's skills dir). An agent that
// has the binary but not the skill previously had no in-band way to learn the
// contract — it had to be handed a path to this repo by a human, and the
// most authoritative-looking file there (the original v1 spec) describes a
// RETIRED model. Every agent holding the binary can now just ask it.
func cmdContract(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("contract", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return fmt.Errorf("unknown argument %q (usage: smith contract [--json])", rest[0])
	}
	text := contractText()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"contract": text,
			"runtimes": scaffold.Runtimes,
			"version":  version,
		})
	}
	fmt.Print(text)
	return nil
}

// contractText renders the contract. The runtime list is interpolated from
// scaffold.Runtimes rather than restated, so it cannot drift from the code that
// enforces it (TestContractRuntimesMatchScaffold pins this).
func contractText() string {
	return `# The smith tool contract

A smith tool is a CLI you adopt into a project behind a trust gate. This is the
contract it must satisfy, and — just as important — the limits that are real
versus the ones that only look real.

## Shape

- One repo per tool: source + ` + "`manifest.yaml`" + ` + ` + "`selftest.sh`" + ` + ` + "`SKILL.md`" + `.
- Runtimes: ` + strings.Join(scaffold.Runtimes, " | ") + `.
- ` + "`SKILL.md`" + ` frontmatter needs a ` + "`description:`" + ` that names the concrete
  situations to reach for the tool. That string is the trigger an agent matches
  on, and it is emitted into agent instruction files — write it for a stranger.

## CLI behavior

- ` + "`<tool> --json [args]`" + ` prints structured JSON on stdout. Never prose, never
  progress chatter. This is the machine path agents use.
- Support ` + "`--quiet`" + `, ` + "`--version`" + `, ` + "`--help`" + `. Be non-interactive: never prompt.
- Errors go to stderr. Exit 0 ok, 1 runtime error, 2 usage error.
- Route all output through one emit choke point so the ` + "`--json`" + ` contract can't
  drift between code paths.

## The self-test is the gate

` + "`smith use`" + ` runs ` + "`selftest.sh`" + ` **sandboxed, before vendoring**: no network,
writes confined to a scratch CWD + TMP, sanitized environment. It must exit
non-zero on real breakage — a self-test that only proves "it runs" passes the
gate and fails human review.

## What the sandbox does NOT constrain (read this before designing around it)

The sandbox applies to the **self-test at adopt time only**. Once adoption is
committed and ` + "`smith install`" + ` runs, your tool is a plain executable on the
project PATH with the user's full privileges.

So a tool may do anything a shell can: download large files, start a daemon,
talk to the network, use a local model. None of that disqualifies it. The single
requirement is that ` + "`selftest.sh`" + ` proves something meaningful **offline**:
argument parsing, ` + "`--json`" + ` shape, exit codes, and graceful failure when the
external thing is absent.

Two honest limits to design with:

- smith pins and hashes your **vendored source**, not what your tool fetches
  later. Pin a tool that downloads weights on first run and you have pinned the
  fetcher, not the weights. Reviewers should know that.
- A tool whose real work can't be exercised offline shifts more weight onto
  human review, because the self-test proves less. That's a tradeoff, not a
  prohibition.

## Sanctioned patterns for things that don't fit a single CLI

- **Heavy or long-running service** (daemon, resident model, socket server,
  launchd/systemd unit): ship the service as its own ordinary project, and make
  the smith tool a thin client that speaks ` + "`--json`" + ` to it. The client is what
  gets adopted and reviewed; the service you install like any other daemon. This
  keeps the reviewable surface small without limiting what the system does.
- **An existing third-party CLI** you just want on PATH: don't wrap it. Use
  ` + "`smith use --mise <spec>`" + ` (e.g. ` + "`ubi:owner/repo`" + `, ` + "`node@20`" + `), which pins it
  in ` + "`mise.toml`" + ` with no vendored source.

## Which limits are load-bearing

Load-bearing — do not route around these:

- The self-test runs sandboxed and offline. This IS the trust gate.
- Adoption is a reviewed commit. Agents propose; humans approve and merge.
- Vendored source is content-hashed and re-verified at install.

Not load-bearing — unbuilt or incidental, argue with them freely:

- **No Node/Deno runtime.** Not a principled exclusion — an unbuilt feature.
  (Note ` + "`smith use --mise node@20`" + ` already provides Node as a *tool*.)
- **Historical docs.** ` + "`docs/history/`" + ` and ` + "`ROADMAP.md`" + ` in the smith repo
  describe the original v1 model (a personal signed index, ` + "`register`" + `,
  ` + "`promote`" + `, Tier 0/quarantine, ` + "`toolsmith-spec.md`" + `). All retired. Do not
  follow them.

Authoritative sources, in order: this output, then the cli-creator skill, then
the smith README. Anything else in that repo is history.
`
}
