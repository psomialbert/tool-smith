package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"smith/internal/scaffold"
)

func TestAllowlistPermits(t *testing.T) {
	pats := []string{"github.com/acme/*", "github.com/tools/exact"}
	allow := []struct{ src, exp string }{
		{"acme/greet", "https://github.com/acme/greet"},
		{"github:acme/x", "https://github.com/acme/x"},
		{"https://github.com/acme/y.git", "https://github.com/acme/y.git"},
		{"tools/exact", "https://github.com/tools/exact"},
	}
	for _, c := range allow {
		if !allowlistPermits(pats, c.src, c.exp) {
			t.Errorf("expected permit for %q (%q)", c.src, c.exp)
		}
	}
	deny := []struct{ src, exp string }{
		{"evil/tool", "https://github.com/evil/tool"},
		{"github.com/tools/other", "https://github.com/tools/other"},
	}
	for _, c := range deny {
		if allowlistPermits(pats, c.src, c.exp) {
			t.Errorf("expected deny for %q", c.src)
		}
	}
	// Empty policy is enforced by the caller (len==0 => no check); the matcher
	// itself denies when nothing matches.
	if allowlistPermits(nil, "x", "x") {
		t.Error("empty patterns should not match")
	}
}

func TestUseRefusedByAllowlist(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	p := testPaths(t)
	// Provision an allowlist in SMITH_HOME that excludes our test repo.
	os.WriteFile(filepath.Join(p.Home, "config.yaml"),
		[]byte("allowlist:\n  repos: [\"github.com/approved/*\"]\n"), 0o644)

	repo := filepath.Join(t.TempDir(), "greet-repo")
	if err := scaffold.Create(repo, scaffold.Manifest{
		Name: "greet", Version: "0.1.0", Description: "x", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo, "v0.1.0")

	t.Chdir(t.TempDir())
	err := cmdUse(p, []string{repo + "@v0.1.0"})
	if err == nil {
		t.Fatal("expected adoption to be blocked by the allowlist")
	}
	// Nothing should have been vendored.
	if _, e := os.Stat(filepath.Join(smithDir, "tools", "greet")); e == nil {
		t.Fatal("blocked adoption still vendored the tool")
	}
}
