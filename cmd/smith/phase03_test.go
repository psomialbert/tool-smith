package main

import (
	"path/filepath"
	"testing"

	"smith/internal/index"
	"smith/internal/store"
)

func TestMiseToGoPlatform(t *testing.T) {
	cases := map[string]string{
		"macos-arm64":  "darwin-arm64",
		"macos-x64":    "darwin-amd64",
		"linux-x64":    "linux-amd64",
		"linux-arm64":  "linux-arm64",
		"linux-x86_64": "linux-amd64",
		"weird":        "weird",
	}
	for in, want := range cases {
		if got := miseToGoPlatform(in); got != want {
			t.Errorf("miseToGoPlatform(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEffectiveArtifactsPrefersIndexThenLocalPins(t *testing.T) {
	p := testPaths(t)

	// Index pins present -> used directly.
	e := index.Entry{Name: "a", Version: "1", Artifacts: []index.Artifact{{Path: "bin/a", Sha256: "idx"}}}
	if got := effectiveArtifacts(e, "/nonexistent"); len(got) != 1 || got[0].Sha256 != "idx" {
		t.Fatalf("expected index artifacts, got %+v", got)
	}

	// No index pins, local pins present -> local pins used.
	dir := filepath.Join(p.StoreQuarantine, "b", "1")
	if err := store.WritePins(dir, []store.LocalPin{{Path: "bin/b", Sha256: "local"}}); err != nil {
		t.Fatal(err)
	}
	stub := index.Entry{Name: "b", Version: "1", Locked: true}
	got := effectiveArtifacts(stub, dir)
	if len(got) != 1 || got[0].Path != "bin/b" || got[0].Sha256 != "local" {
		t.Fatalf("expected local pins, got %+v", got)
	}

	// Neither -> nil (an unfetched stub).
	if got := effectiveArtifacts(index.Entry{Name: "c", Version: "1", Locked: true}, filepath.Join(p.StoreQuarantine, "c", "1")); got != nil {
		t.Fatalf("expected nil for unfetched stub, got %+v", got)
	}
}
