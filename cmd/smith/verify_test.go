package main

import (
	"os"
	"path/filepath"
	"testing"
)

func seedPinnedTool(t *testing.T) {
	t.Helper()
	sum := seedBashTool(t, "greet") // creates .smith/tools/greet + files, returns sha
	saveProjLock(ProjLock{Tools: map[string]ProjTool{
		"greet": {Source: "x/greet", Version: "v1", Sha256: sum, Entrypoint: "greet", Runtime: "bash"},
	}})
}

func TestVerifyPassesWhenIntact(t *testing.T) {
	t.Chdir(t.TempDir())
	seedPinnedTool(t)
	if err := cmdVerify(testPaths(t), nil); err != nil {
		t.Fatalf("verify should pass on an intact bundle: %v", err)
	}
}

func TestVerifyFailsOnDrift(t *testing.T) {
	t.Chdir(t.TempDir())
	seedPinnedTool(t)
	// Tamper the vendored source.
	os.WriteFile(filepath.Join(smithDir, "tools", "greet", "greet"),
		[]byte("#!/bin/bash\necho tampered\n"), 0o755)
	if err := cmdVerify(testPaths(t), nil); err == nil {
		t.Fatal("verify must fail on vendored-source drift")
	}
}

func TestVerifyFailsOnMissing(t *testing.T) {
	t.Chdir(t.TempDir())
	seedPinnedTool(t)
	os.RemoveAll(filepath.Join(smithDir, "tools", "greet"))
	if err := cmdVerify(testPaths(t), nil); err == nil {
		t.Fatal("verify must fail when a pinned tool's vendored dir is missing")
	}
}

func TestVerifyFrozenFailsOnOrphan(t *testing.T) {
	t.Chdir(t.TempDir())
	seedPinnedTool(t)
	// A vendored dir with no pin.
	os.MkdirAll(filepath.Join(smithDir, "tools", "stray"), 0o755)

	if err := cmdVerify(testPaths(t), nil); err != nil {
		t.Fatalf("default verify tolerates an orphan (warning): %v", err)
	}
	if err := cmdVerify(testPaths(t), []string{"--frozen"}); err == nil {
		t.Fatal("--frozen must fail on an orphan vendored dir")
	}
}

// TestVendoredOrphansFlagsMiseDir guards the hole where a vendored dir named
// after a mise-pinned tool escaped both integrity checks: it's skipped by the
// hash check (external, no vendored source) AND was not reported as an orphan
// (a lock entry exists), so `verify --frozen` — documented as "tree must match
// the lockfile exactly" — silently tolerated an unpinned directory.
func TestVendoredOrphansFlagsMiseDir(t *testing.T) {
	t.Chdir(t.TempDir())
	os.MkdirAll(filepath.Join(smithDir, "tools", "pup", "sub"), 0o755)
	os.WriteFile(filepath.Join(smithDir, "tools", "pup", "evil.sh"), []byte("x"), 0o755)
	lock := ProjLock{Tools: map[string]ProjTool{
		"pup": {Source: "x/pup", Version: "v1", Runtime: "mise"}, // external: must have no vendored dir
	}}
	orphans, err := vendoredOrphans(lock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, o := range orphans {
		if o == "pup" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a vendored dir for a mise tool must be reported as an orphan, got %v", orphans)
	}
}

// TestVendoredOrphansNoToolsDir: a missing .smith/tools is legitimately zero
// orphans (not an error), but any other read failure must NOT be swallowed —
// verify --frozen must fail closed, never claim a match it couldn't check.
func TestVendoredOrphansNoToolsDir(t *testing.T) {
	t.Chdir(t.TempDir()) // no .smith/tools at all
	orphans, err := vendoredOrphans(ProjLock{Tools: map[string]ProjTool{}})
	if err != nil || len(orphans) != 0 {
		t.Fatalf("missing tools dir → (nil, nil), got (%v, %v)", orphans, err)
	}
}
