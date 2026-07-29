package main

// Agent discovery targets: how a project's adopted tools get advertised to
// whatever agent is working in the project. smith's core is agent-agnostic (a
// tool is a CLI on PATH honoring `<tool> --json`); this file is the layer that
// tells a specific agent those tools exist.

import (
	"path/filepath"
	"strings"
)

// toolEntry is one adopted tool as presented to an agent. Shared by `smith
// lens` and every agent target so an emitted block can never disagree with
// lens — they render the same data.
type toolEntry struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Runtime  string `json:"runtime"`
	External bool   `json:"external"`
	Invoke   string `json:"invoke"`
	Trigger  string `json:"trigger"`
}

// projectToolIndex builds the agent-facing index from the project lockfile.
// Order is deterministic (sortedToolNames) so regenerated output is stable and
// repeated emission is a no-op.
func projectToolIndex(lock ProjLock) []toolEntry {
	names := sortedToolNames(lock)
	out := make([]toolEntry, 0, len(names))
	for _, name := range names {
		t := lock.Tools[name]
		// Vendored smith tools honor the `<tool> --json` contract; external mise
		// CLIs don't promise it, so don't advertise it for them.
		invoke := name + " --json …"
		if t.isMise() {
			invoke = name
		}
		e := toolEntry{
			Name: name, Version: t.Version, Runtime: t.Runtime,
			External: t.isMise(), Invoke: invoke,
		}
		switch {
		case t.isMise():
			e.Trigger = "external CLI via mise (" + t.Source + ") — no source reviewed"
		default:
			if d := skillDescription(filepath.Join(smithDir, "tools", name, "SKILL.md")); d != "" {
				e.Trigger = d
			} else {
				e.Trigger = "(no trigger described)"
			}
		}
		out = append(out, e)
	}
	return out
}

// triggerMaxLen caps an emitted trigger: long enough for a real trigger
// sentence, short enough that one tool cannot dominate an agent's context.
const triggerMaxLen = 300

// sanitizeTrigger makes a tool's description safe to embed as a table row in an
// agent instruction file. The description is reviewed at adoption, so this
// narrows rather than replaces that trust boundary — but it must hold even for a
// hostile string:
//
//   - HTML comment syntax is stripped, so a description cannot forge the
//     managed-block end marker and inject content outside the block (where
//     regeneration would never overwrite it).
//   - newlines/whitespace runs collapse, keeping the row a single table row.
//   - `|` is escaped so it cannot forge table columns.
//   - length is capped.
func sanitizeTrigger(s string) string {
	s = strings.NewReplacer("<!--", "", "-->", "").Replace(s)
	s = strings.Join(strings.Fields(s), " ") // collapses \n, \r\n, tabs, runs
	s = strings.ReplaceAll(s, "|", `\|`)
	if len(s) > triggerMaxLen {
		// "…" (U+2026) is 3 bytes in UTF-8, so reserve room for all of them —
		// s[:triggerMaxLen-1] would leave the result 2 bytes over cap.
		s = strings.TrimSpace(s[:triggerMaxLen-3]) + "…"
	}
	return s
}
