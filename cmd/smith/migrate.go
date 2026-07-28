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
		switch a {
		case "--quiet", "-q":
			quiet = true
		default:
			// Reject unknown flags rather than ignoring them: a user typing
			// `smith migrate --dry-run` expecting a no-op would otherwise trigger
			// a real (albeit reversible) migration.
			return fmt.Errorf("unknown argument %q (usage: smith migrate [--quiet]); migrate has no dry-run — it backs up reversibly", a)
		}
	}
	say := func(format string, a ...any) {
		if !quiet {
			fmt.Printf(format+"\n", a...)
		}
	}

	present, staleKeys, derr := diagnoseV1Home(p)
	if derr != nil {
		return fmt.Errorf("reading %s: %w", filepath.Join(p.Home, "config.yaml"), derr)
	}
	if len(present) == 0 && len(staleKeys) == 0 {
		say("smith: already on the v2 layout; nothing to migrate")
		return nil
	}
	moved, dropped, backup, err := applyV1Migrate(p)
	// Report what already happened even if a later step failed: the dir backups
	// run before the config strip, so on a strip error the user must still be
	// told their v1 dirs were relocated (else they can't find them).
	for _, name := range moved {
		say("  moved v1 %s → %s", name, backup)
	}
	if len(moved) > 0 {
		say("smith: backed up %d retired item(s) to %s (safe to delete once you're happy)", len(moved), backup)
	}
	if len(dropped) > 0 {
		say("  stripped retired config key(s): %s", strings.Join(dropped, ", "))
	}
	return err
}

// diagnoseV1Home reports retired v1 artifacts present in SMITH_HOME and dead
// config keys, without mutating anything (used by `smith doctor`).
func diagnoseV1Home(p config.Paths) (present []string, staleKeys []string, err error) {
	for _, name := range v1HomeArtifacts {
		if _, err := os.Lstat(filepath.Join(p.Home, name)); err == nil {
			present = append(present, name)
		}
	}
	// Surface a genuine read error (e.g. permission denied) instead of reporting
	// "no stale keys": otherwise doctor's v1 check would read clean while
	// `smith migrate` would later fail on the same unreadable config.
	staleKeys, err = configStaleKeyNames(filepath.Join(p.Home, "config.yaml"))
	return present, staleKeys, err
}

// applyV1Migrate backs up retired dirs to a timestamped dir and strips dead
// config keys, returning what it did. A fixed (per-day) backup name keeps it
// deterministic and re-runnable.
func applyV1Migrate(p config.Paths) (moved []string, dropped []string, backup string, err error) {
	present, _, _ := diagnoseV1Home(p)
	if len(present) > 0 {
		backup = filepath.Join(p.Home, ".v1-backup-"+time.Now().Format("20060102"))
		if err = os.MkdirAll(backup, 0o755); err != nil {
			return
		}
		for _, name := range present {
			// Never clobber an existing backup — a same-day re-run (e.g. via
			// `smith doctor --fix`) must not destroy a copy the user may have
			// restored from. Pick the first free "<name>", "<name>.1", ... slot.
			dst := filepath.Join(backup, name)
			for i := 1; ; i++ {
				if _, statErr := os.Lstat(dst); os.IsNotExist(statErr) {
					break
				}
				dst = filepath.Join(backup, fmt.Sprintf("%s.%d", name, i))
			}
			if err = os.Rename(filepath.Join(p.Home, name), dst); err != nil {
				err = fmt.Errorf("backing up %s: %w", name, err)
				return
			}
			moved = append(moved, name)
		}
	}
	dropped, err = staleConfigKeys(filepath.Join(p.Home, "config.yaml"))
	return
}

// configStaleKeyNames returns retired top-level config.yaml keys without
// writing. Missing/empty/unparseable config → no keys.
func configStaleKeyNames(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, nil // unparseable: leave it alone
	}
	var dropped []string
	for k := range m {
		if !v2ConfigKeys[k] {
			dropped = append(dropped, k)
		}
	}
	sort.Strings(dropped)
	return dropped, nil
}

// staleConfigKeys removes retired top-level keys from config.yaml, rewriting it
// in place, and returns the names it dropped. Comments are not preserved (yaml
// round-trip), acceptable for a one-time migration of a small settings file.
func staleConfigKeys(path string) ([]string, error) {
	dropped, err := configStaleKeyNames(path)
	if err != nil || len(dropped) == 0 {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, nil
	}
	for _, k := range dropped {
		delete(m, k)
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	// Atomic rewrite: a crash mid-write must not leave a truncated config.yaml
	// that LoadFileConfig would then reject, blocking `use`/`install` until the
	// user hand-repairs a file smith corrupted.
	if err := writeFileAtomic(path, out, 0o644); err != nil {
		return nil, err
	}
	return dropped, nil
}
