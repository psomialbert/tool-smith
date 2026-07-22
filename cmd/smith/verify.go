package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

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
		Status string `json:"status"` // ok | mismatch | missing | error | orphan
		Detail string `json:"detail,omitempty"`
	}
	var results []result
	bad := 0

	names := sortedToolNames(lock)
	for _, name := range names {
		t := lock.Tools[name]
		dir := filepath.Join(smithDir, "tools", name)
		if _, err := os.Stat(dir); err != nil {
			results = append(results, result{name, "missing", "no vendored source at " + dir})
			bad++
			continue
		}
		sum, herr := bundleHash(dir, sourceFiles(t.Runtime, t.Entrypoint, name))
		switch {
		case herr != nil:
			results = append(results, result{name, "error", herr.Error()})
			bad++
		case sum != t.Sha256:
			results = append(results, result{name, "mismatch", "vendored sha ≠ pin — review and re-adopt"})
			bad++
		default:
			results = append(results, result{name, "ok", ""})
		}
	}

	// Vendored dirs with no pin: a warning by default, a failure with --frozen.
	orphans := vendoredOrphans(lock)
	for _, o := range orphans {
		results = append(results, result{o, "orphan", "vendored but not in tools.yaml"})
		if *frozen {
			bad++
		}
	}

	if *asJSON {
		return jsonPrint(map[string]any{"results": results, "frozen": *frozen, "ok": bad == 0})
	}
	if len(names) == 0 && len(orphans) == 0 {
		fmt.Println("no adopted tools to verify")
		return nil
	}
	for _, r := range results {
		if r.Status == "ok" {
			fmt.Printf("  ✓ %s\n", r.Tool)
		} else {
			fmt.Printf("  ✗ %s: %s\n", r.Tool, r.Detail)
		}
	}
	if bad > 0 {
		return fmt.Errorf("verify failed: %d problem(s)", bad)
	}
	fmt.Printf("verified %d tool(s): vendored source matches pins\n", len(names))
	return nil
}

// vendoredOrphans returns vendored tool dirs that have no entry in the lockfile.
func vendoredOrphans(lock ProjLock) []string {
	entries, err := os.ReadDir(filepath.Join(smithDir, "tools"))
	if err != nil {
		return nil
	}
	var orphans []string
	for _, e := range entries {
		if e.IsDir() {
			if _, ok := lock.Tools[e.Name()]; !ok {
				orphans = append(orphans, e.Name())
			}
		}
	}
	return orphans
}
