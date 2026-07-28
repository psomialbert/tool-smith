package main

import (
	"flag"
	"fmt"
	"path/filepath"

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
	names := sortedToolNames(lock)

	type entry struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Runtime  string `json:"runtime"`
		External bool   `json:"external"`
		Invoke   string `json:"invoke"`
		Trigger  string `json:"trigger"`
	}
	entries := make([]entry, 0, len(names))
	for _, name := range names {
		t := lock.Tools[name]
		// Vendored smith tools honor the `<tool> --json` contract; external mise
		// CLIs don't promise it, so don't advertise it for them.
		invoke := name + " --json …"
		if t.isMise() {
			invoke = name
		}
		e := entry{Name: name, Version: t.Version, Runtime: t.Runtime, External: t.isMise(), Invoke: invoke}
		if t.isMise() {
			e.Trigger = "external CLI via mise (" + t.Source + ") — no source reviewed"
		} else if d := skillDescription(filepath.Join(smithDir, "tools", name, "SKILL.md")); d != "" {
			e.Trigger = d
		} else {
			e.Trigger = "(no trigger described)"
		}
		entries = append(entries, e)
	}

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
