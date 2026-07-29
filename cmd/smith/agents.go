package main

// Agent discovery targets: how a project's adopted tools get advertised to
// whatever agent is working in the project. smith's core is agent-agnostic (a
// tool is a CLI on PATH honoring `<tool> --json`); this file is the layer that
// tells a specific agent those tools exist.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
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
	s = stripHTMLComments(s)
	s = strings.Join(strings.Fields(s), " ") // collapses \n, \r\n, tabs, runs
	s = strings.ReplaceAll(s, "|", `\|`)
	if len(s) > triggerMaxLen {
		// "…" (U+2026) is 3 bytes in UTF-8, so reserve room for all of them —
		// s[:triggerMaxLen-1] would leave the result 2 bytes over cap.
		s = strings.TrimSpace(truncateValidUTF8(s, triggerMaxLen-3)) + "…"
	}
	return s
}

// stripHTMLComments removes "<!--" and "-->" substrings, repeating to a
// fixpoint. A single pass is not idempotent: "<!-<!---->->" sanitizes to
// "<!-->" after one pass, which still contains "<!--". Each pass strictly
// shortens the string (or leaves it unchanged), so this always terminates.
func stripHTMLComments(s string) string {
	for {
		next := strings.NewReplacer("<!--", "", "-->", "").Replace(s)
		if next == s {
			return next
		}
		s = next
	}
}

// truncateValidUTF8 returns a prefix of s at most n bytes, trimmed back off
// any rune that a raw byte slice at n would otherwise cut in half — a plain
// s[:n] can land mid-rune and emit invalid UTF-8 (e.g. the real "external CLI
// via mise (…) — no source reviewed" trigger contains a multi-byte em dash).
func truncateValidUTF8(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(s) > n {
		s = s[:n]
	}
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// escapeTableCell makes a value safe to embed as a table cell in the
// rendered block. Unlike sanitizeTrigger, it does not collapse whitespace or
// cap length — Name, Version, and Invoke aren't free-text descriptions — but
// it must still close the same two holes sanitizeTrigger closes for Trigger:
//
//   - HTML comment syntax is stripped (reusing stripHTMLComments' fixpoint
//     loop) so a hostile Name/Version/Invoke — e.g. a git tag like
//     "v1.0<!-- smith:end -->", reachable because Version is unvalidated on
//     the adopt path — cannot forge the managed-block end marker.
//     upsertManagedBlock locates the FIRST end marker after the begin marker
//     and treats everything past it as untouched tail; a forged marker here
//     would truncate the managed block early and let attacker-controlled
//     content become a permanent fixture outside it, immune to regeneration.
//   - `|` is escaped so it cannot forge table columns.
func escapeTableCell(s string) string {
	s = stripHTMLComments(s)
	return strings.ReplaceAll(s, "|", `\|`)
}

// renderAgentBlock renders the project's adopted tools as the smith-managed
// block for an agent instruction file. The per-row invoke column and the
// external marker are load-bearing, not cosmetic: `smith lens` distinguishes
// vendored tools (which honor `--json`) from `--mise` externals (which never
// promised it and whose source was never reviewed). A blanket "--json" header
// would be wrong for externals and would tell an agent an unreviewed
// third-party CLI is equivalent to a reviewed, vendored one.
//
// Every cell is escaped for `|` before being written, regardless of whether
// upstream validation happens to forbid it today (Name is constrained by
// scaffold.ValidToolName, but Version reaches here straight from a git ref —
// a legal git tag can contain "|", and the lockfile is hand-editable). The
// renderer owns the table's structure, so it stays correct for any
// toolEntry rather than relying on invariants enforced elsewhere.
func renderAgentBlock(tools []toolEntry) string {
	var b strings.Builder
	b.WriteString(agentBlockBeginPrefix + " — generated by `smith install`; edits inside are overwritten -->\n")
	b.WriteString("## Project tools (smith)\n\n")
	b.WriteString("These are on PATH for this project. Run `smith lens` for the always-fresh index.\n\n")
	if len(tools) == 0 {
		b.WriteString("No tools adopted yet. `smith use <repo>@<ver>` adopts one.\n")
		b.WriteString(agentBlockEnd + "\n")
		return b.String()
	}
	b.WriteString("| tool | version | invoke | trigger |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, t := range tools {
		invoke := "`" + escapeTableCell(t.Invoke) + "`"
		if t.External {
			invoke += " (external, unreviewed)"
		}
		b.WriteString("| " + escapeTableCell(t.Name) + " | " + escapeTableCell(t.Version) + " | " + invoke + " | " + sanitizeTrigger(t.Trigger) + " |\n")
	}
	b.WriteString(agentBlockEnd + "\n")
	return b.String()
}

const (
	// agentBlockBeginPrefix is matched as a PREFIX, not an exact string, so the
	// trailing "generated by ..." note can change later without orphaning blocks
	// already written into users' files.
	agentBlockBeginPrefix = "<!-- smith:begin"
	agentBlockEnd         = "<!-- smith:end -->"
)

// upsertManagedBlock replaces the smith-managed block in an existing file, or
// appends it when absent. Content outside the markers is never touched.
//
// It refuses rather than risks data loss in two cases: a symlinked target
// (writeFileAtomic renames into place, which would replace the symlink instead
// of writing through it) and a file containing more than one begin marker (no
// way to tell which block is authoritative). It also refuses a missing file —
// `smith install` never creates an agent instruction file.
func upsertManagedBlock(path, block string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; refusing to write (an atomic rename would replace the link itself)", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(raw)

	if n := strings.Count(text, agentBlockBeginPrefix); n > 1 {
		return fmt.Errorf("%s has %d smith blocks; remove the extras so there is one to update", path, n)
	}

	var out string
	begin := strings.Index(text, agentBlockBeginPrefix)
	switch {
	case begin < 0:
		// Append, separated by exactly one blank line, matching the file's
		// existing line-ending convention. strings.TrimRight(text, "\n") alone
		// would strip only the bare "\n" of a CRLF file's trailing "\r\n",
		// leaving a dangling "\r" and then splicing in an LF-only blank line —
		// mutating a byte outside the markers on the very first append.
		newline := "\n"
		if strings.Contains(text, "\r\n") {
			newline = "\r\n"
		}
		trimmed := strings.TrimRight(text, "\r\n")
		out = trimmed + newline + newline + strings.TrimRight(block, "\n") + newline
	default:
		endIdx := strings.Index(text[begin:], agentBlockEnd)
		if endIdx < 0 {
			return fmt.Errorf("%s has an unterminated smith block (missing %s)", path, agentBlockEnd)
		}
		tail := text[begin+endIdx+len(agentBlockEnd):]
		out = text[:begin] + strings.TrimRight(block, "\n") + tail
	}
	if out == text {
		return nil // already current — don't touch the file at all
	}
	return writeFileAtomic(path, []byte(out), 0o644)
}
