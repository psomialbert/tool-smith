package main

import (
	"flag"
	"fmt"

	"smith/internal/config"
)

// cmdLens prints a project-scoped, agent-facing index of the tools THIS project
// has adopted: each tool's trigger (its SKILL.md frontmatter description) and
// how to invoke it. Unlike the globally-exported per-tool skills, this answers
// "what's adopted *here*". On-demand and always fresh (reads .smith/tools.yaml
// + vendored SKILL.md). Markdown by default; --json for machines.
func cmdLens(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("lens", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lock, err := loadProjLock()
	if err != nil {
		return err
	}
	entries := projectToolIndex(lock)

	if *asJSON {
		return jsonPrint(map[string]any{"tools": entries})
	}
	if len(entries) == 0 {
		fmt.Println("No smith tools adopted in this project. Try `smith search` or `smith use <repo>`.")
		return nil
	}
	fmt.Println("# Tools adopted in this project (smith)")
	fmt.Println()
	fmt.Println("These are on PATH after `smith install`. Machine path: `<tool> --json`. Reach for one before")
	fmt.Println("hand-writing logic it already covers.")
	fmt.Println()
	for _, e := range entries {
		tag := ""
		if e.External {
			tag = " · external (mise)"
		}
		fmt.Printf("## %s  (%s %s%s)\n\n%s\n\n`%s`\n\n", e.Name, e.Runtime, e.Version, tag, e.Trigger, e.Invoke)
	}
	return nil
}
