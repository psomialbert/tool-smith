package main

import (
	"os"
	"path/filepath"
	"testing"

	"smith/internal/index"
	"smith/internal/scaffold"
)

func TestBumpPatch(t *testing.T) {
	cases := map[string]string{"0.1.0": "0.1.1", "1.2.9": "1.2.10", "3": "4", "0.1.0-rc": ""}
	for in, want := range cases {
		got, err := bumpPatch(in)
		if want == "" {
			if err == nil {
				t.Errorf("bumpPatch(%q) should error", in)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("bumpPatch(%q) = %q (err %v), want %q", in, got, want, want)
		}
	}
}

func TestUpgradeAuthoredSeedsDraft(t *testing.T) {
	requireSandbox(t)
	p := testPaths(t)
	// Author + register v0.1.0.
	if err := scaffold.Create(draftDir(p, "demo"), scaffold.Manifest{
		Name: "demo", Version: "0.1.0", Description: "d", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRegister(p, []string{"demo"}); err != nil {
		t.Fatal(err)
	}

	// Upgrade (auto patch-bump) reseeds the draft at 0.1.1.
	if err := cmdUpgrade(p, []string{"demo"}); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	m, err := scaffold.LoadManifest(draftDir(p, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "0.1.1" {
		t.Fatalf("draft version = %q, want 0.1.1", m.Version)
	}
	// Draft carries the entrypoint from the source bundle.
	if _, err := os.Stat(filepath.Join(draftDir(p, "demo"), "demo")); err != nil {
		t.Fatal("seeded draft missing entrypoint")
	}

	// Re-register at the new version; both coexist.
	if err := cmdRegister(p, []string{"demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Load(p.IndexTools, "demo", "0.1.0"); err != nil {
		t.Fatal("original version gone")
	}
	if _, err := index.Load(p.IndexTools, "demo", "0.1.1"); err != nil {
		t.Fatal("upgraded version not registered")
	}

	// Explicit --version colliding with an existing one is refused.
	if err := cmdUpgrade(p, []string{"demo", "--version", "0.1.0"}); err == nil {
		t.Fatal("upgrade to an existing version should be refused")
	}
}
