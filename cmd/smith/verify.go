package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"smith/internal/config"
)

// cmdVerify re-hashes each vendored tool bundle against its pin in
// .smith/tools.yaml and fails on any mismatch — the go.sum-style integrity gate,
// as a single-purpose CI command (cf. `smith doctor`'s broader report). Without
// --frozen, vendored dirs not in the lockfile are a warning; with --frozen the
// tree must match the lockfile exactly (orphans fail too).
func cmdVerify(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	frozen := fs.Bool("frozen", false, "also fail on vendored dirs not in the lockfile (exact match)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lock, err := loadProjLock()
	if err != nil {
		return err
	}

	type result struct {
		Tool   string `json:"tool"`
		Status string `json:"status"` // ok | mismatch | missing | error | external | orphan
		Detail string `json:"detail,omitempty"`
	}
	var results []result
	bad := 0
	verified := 0 // vendored tools that actually passed the hash check

	names := sortedToolNames(lock)
	for _, name := range names {
		t := lock.Tools[name]
		if t.isMise() {
			// External tool: no vendored source to hash — pinned in mise.toml.
			results = append(results, result{name, "external", "external (mise) — not vendored"})
			continue
		}
		dir := filepath.Join(smithDir, "tools", name)
		if _, err := os.Stat(dir); err != nil {
			results = append(results, result{name, "missing", "no vendored source at " + dir})
			bad++
			continue
		}
		src, serr := vendoredSourceFiles(dir, t, name)
		if serr != nil {
			// A `sources:` list that no longer resolves against the vendored tree
			// (a listed file deleted, a symlink introduced) is an integrity
			// failure, not a crash — report it like any other bad tool.
			results = append(results, result{name, "error", serr.Error()})
			bad++
			continue
		}
		sum, herr := bundleHash(dir, src)
		extra, xerr := unexpectedFiles(dir, src)
		switch {
		case herr != nil:
			results = append(results, result{name, "error", herr.Error()})
			bad++
		case sum != t.Sha256:
			results = append(results, result{name, "mismatch", "vendored sha ≠ pin — review and re-adopt"})
			bad++
		case xerr != nil:
			results = append(results, result{name, "error", "scanning vendored dir: " + xerr.Error()})
			bad++
		case len(extra) > 0:
			// Files present in the vendored dir but outside the pinned source
			// set are not covered by the hash yet can hijack execution — treat
			// them as tampering, not just a --frozen concern.
			results = append(results, result{name, "mismatch", "un-pinned files in vendored dir: " + strings.Join(extra, ", ")})
			bad++
		default:
			results = append(results, result{name, "ok", ""})
			verified++
		}
	}

	// Vendored dirs with no pin: a warning by default, a failure with --frozen.
	orphans, err := vendoredOrphans(lock)
	if err != nil {
		return err
	}
	for _, o := range orphans {
		results = append(results, result{o, "orphan", "vendored but not in tools.yaml"})
		if *frozen {
			bad++
		}
	}

	if *asJSON {
		if err := jsonPrint(map[string]any{"results": results, "frozen": *frozen, "ok": bad == 0}); err != nil {
			return err
		}
		// The integrity gate must fail closed in machine-readable mode too:
		// exit nonzero on any problem so CI doesn't stay green on `ok:false`.
		if bad > 0 {
			return fmt.Errorf("verify failed: %d problem(s)", bad)
		}
		return nil
	}
	if len(names) == 0 && len(orphans) == 0 {
		fmt.Println("no adopted tools to verify")
		return nil
	}
	for _, r := range results {
		switch r.Status {
		case "ok":
			fmt.Printf("  ✓ %s\n", r.Tool)
		case "external":
			fmt.Printf("  • %s: %s\n", r.Tool, r.Detail)
		case "orphan":
			// A warning by default (exit 0), a failure only under --frozen. Use a
			// warning glyph so the line isn't a ✗ next to a green exit.
			glyph := "⚠"
			if *frozen {
				glyph = "✗"
			}
			fmt.Printf("  %s %s: %s\n", glyph, r.Tool, r.Detail)
		default:
			fmt.Printf("  ✗ %s: %s\n", r.Tool, r.Detail)
		}
	}
	if bad > 0 {
		return fmt.Errorf("verify failed: %d problem(s)", bad)
	}
	// Count only vendored tools actually hashed — not external (mise) tools,
	// which carry no vendored source to match against a pin.
	fmt.Printf("verified %d tool(s): vendored source matches pins\n", verified)
	return nil
}

// vendoredOrphans returns entries under .smith/tools/ that have no business
// being there: unpinned tool dirs, dirs pinned as external (mise) tools which
// must have NO vendored source, AND stray plain files (e.g. an executable
// dropped directly under tools/, outside any pinned tool's unexpectedFiles
// scan). Under --frozen the tree must match the lockfile exactly, so all are
// reported. A missing tools dir is legitimately zero orphans; any other read
// error is returned (fail closed) rather than swallowed — the integrity gate
// must never claim a match it couldn't verify.
func vendoredOrphans(lock ProjLock) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(smithDir, "tools"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning %s/tools: %w", smithDir, err)
	}
	var orphans []string
	for _, e := range entries {
		if e.IsDir() {
			// An external (mise) tool is fetched via mise and vendors no source,
			// so a dir bearing its name is unexpected — treat it as an orphan
			// even though a lock entry exists.
			if t, ok := lock.Tools[e.Name()]; !ok || t.isMise() {
				orphans = append(orphans, e.Name())
			}
			continue
		}
		// A non-directory entry directly under tools/ is never a pinned tool.
		orphans = append(orphans, e.Name())
	}
	return orphans, nil
}
