package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/config"
	"smith/internal/index"
	"smith/internal/provenance"
	"smith/internal/store"
)

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	t.Setenv("SMITH_HOME", t.TempDir())
	p, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeTool registers a quarantined tier-2 tool with one artifact.
func fakeTool(t *testing.T, p config.Paths, name, version string) index.Entry {
	t.Helper()
	dir := filepath.Join(store.QuarantineDir(p.StoreQuarantine, name, version), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := store.HashFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	e := index.Entry{
		Name: name, Version: version, Tier: 2, Origin: "external",
		Source: "test", AddedAt: nowUTC(),
		Artifacts: []index.Artifact{{Path: filepath.Join("bin", name), Sha256: sum}},
	}
	if err := index.Save(p.IndexTools, e); err != nil {
		t.Fatal(err)
	}
	if err := provenance.Append(p.ProvenanceLog, provenance.Entry{
		Event: "add", Tool: name, Version: version, TierTo: provenance.Tier(2), Actor: "test",
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestPromoteDemoteRoundTrip(t *testing.T) {
	p := testPaths(t)
	e := fakeTool(t, p, "widget", "1.0.0")

	if err := promoteEntry(p, e, "test-reviewer"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// Binary moved to trusted, symlinked into bin, index tier 1.
	link := filepath.Join(p.TrustedBin, "widget")
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("trusted bin symlink missing: %v", err)
	}
	trustedRoot, _ := filepath.EvalSymlinks(p.StoreTrusted)
	if !strings.HasPrefix(target, trustedRoot) {
		t.Fatalf("symlink %s escapes trusted store", target)
	}
	got, err := index.Load(p.IndexTools, "widget", "1.0.0")
	if err != nil || got.Tier != 1 {
		t.Fatalf("index tier after promote = %d, err=%v", got.Tier, err)
	}
	if _, err := os.Stat(store.QuarantineDir(p.StoreQuarantine, "widget", "1.0.0")); !os.IsNotExist(err) {
		t.Fatal("quarantine copy still present after promote")
	}

	// Demote mechanics: unlink + move back (mirrors cmdDemote's mutations).
	got.Tier = 1
	var names []string
	for _, a := range got.Artifacts {
		names = append(names, a.Path)
	}
	if err := store.UnlinkBin(p.TrustedBin, p.StoreTrusted, "widget", "1.0.0", names); err != nil {
		t.Fatal(err)
	}
	if err := store.Move(store.TrustedDir(p.StoreTrusted, "widget", "1.0.0"),
		store.QuarantineDir(p.StoreQuarantine, "widget", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("symlink still on PATH after demote")
	}
}

func TestProvenanceChainDetectsTamper(t *testing.T) {
	p := testPaths(t)
	fakeTool(t, p, "a", "1")
	e := fakeTool(t, p, "b", "1")
	if err := promoteEntry(p, e, "rev"); err != nil {
		t.Fatal(err)
	}
	if n, err := provenance.VerifyChain(p.ProvenanceLog); err != nil || n != 3 {
		t.Fatalf("chain verify: n=%d err=%v", n, err)
	}
	// Tamper with the middle entry.
	raw, _ := os.ReadFile(p.ProvenanceLog)
	tampered := strings.Replace(string(raw), `"tool":"b"`, `"tool":"c"`, 1)
	if tampered == string(raw) {
		t.Fatal("test setup: substring not found")
	}
	os.WriteFile(p.ProvenanceLog, []byte(tampered), 0o644)
	if _, err := provenance.VerifyChain(p.ProvenanceLog); err == nil {
		t.Fatal("tampered chain verified clean")
	}
}

func TestVersionResolvePicksHighest(t *testing.T) {
	p := testPaths(t)
	fakeTool(t, p, "tool", "1.9.0")
	fakeTool(t, p, "tool", "1.10.0")
	e, err := index.Resolve(p.IndexTools, "tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if e.Version != "1.10.0" {
		t.Fatalf("resolved %s, want 1.10.0 (numeric compare)", e.Version)
	}
}
