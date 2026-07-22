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
