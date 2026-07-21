package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"smith/internal/config"
)

// v1HomeArtifacts are directories/files the retired personal-index model wrote
// into SMITH_HOME (~/.toolsmith). v2 keeps only sandbox/ and config.yaml, so
// everything here is dead weight once a machine is on v2.
var v1HomeArtifacts = []string{
	"drafts", "provenance", "index", "keys", "store",
	".git", ".gitignore", ".gitattributes",
}

// v2ConfigKeys are the top-level config.yaml keys v2 still understands. Any
// other key (e.g. v1's `transport:`, `signing:`) is retired and stripped on
// migrate. Keep in sync with config.FileConfig.
var v2ConfigKeys = map[string]bool{
	"editor": true, "review": true, "skills": true, "allowlist": true,
}

// cmdMigrate performs machine-level v1 → v2 cleanup of SMITH_HOME: it moves
// retired directories into a timestamped backup (reversible — nothing is
// destroyed) and strips retired keys from config.yaml. Idempotent: a machine
// already on v2 reports "nothing to migrate". Run automatically by
// scripts/install.sh and available as `smith migrate`.
func cmdMigrate(p config.Paths, args []string) error {
	quiet := false
	for _, a := range args {
		if a == "--quiet" || a == "-q" {
			quiet = true
		}
	}
	say := func(format string, a ...any) {
		if !quiet {
			fmt.Printf(format+"\n", a...)
		}
	}

	var present []string
	for _, name := range v1HomeArtifacts {
		if _, err := os.Lstat(filepath.Join(p.Home, name)); err == nil {
			present = append(present, name)
		}
	}
	droppedKeys, cfgErr := staleConfigKeys(filepath.Join(p.Home, "config.yaml"))
	if cfgErr != nil {
		return cfgErr
	}

	if len(present) == 0 && len(droppedKeys) == 0 {
		say("smith: already on the v2 layout; nothing to migrate")
		return nil
	}

	if len(present) > 0 {
		// A fixed backup name keeps migrate deterministic and re-runnable; the
		// day is enough to distinguish accidental double-runs from real history.
		backup := filepath.Join(p.Home, ".v1-backup-"+time.Now().Format("20060102"))
		if err := os.MkdirAll(backup, 0o755); err != nil {
			return err
		}
		for _, name := range present {
			dst := filepath.Join(backup, name)
			if err := os.RemoveAll(dst); err != nil { // idempotent re-run
				return err
			}
			if err := os.Rename(filepath.Join(p.Home, name), dst); err != nil {
				return fmt.Errorf("backing up %s: %w", name, err)
			}
			say("  moved v1 %s → %s", name, backup)
		}
		say("smith: backed up %d retired item(s) to %s (safe to delete once you're happy)", len(present), backup)
	}
	if len(droppedKeys) > 0 {
		say("  stripped retired config key(s): %s", strings.Join(droppedKeys, ", "))
	}
	return nil
}

// staleConfigKeys removes retired top-level keys from config.yaml, rewriting it
// in place, and returns the names it dropped. Missing/empty config → no-op.
// Comments are not preserved (yaml round-trip), which is acceptable for a
// one-time migration of a small settings file.
func staleConfigKeys(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &m); err != nil {
		// Unparseable config: leave it alone rather than clobber it.
		return nil, nil
	}
	var dropped []string
	for k := range m {
		if !v2ConfigKeys[k] {
			dropped = append(dropped, k)
			delete(m, k)
		}
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	sort.Strings(dropped)
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return nil, err
	}
	return dropped, nil
}
