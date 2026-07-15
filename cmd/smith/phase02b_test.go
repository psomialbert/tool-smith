package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"smith/internal/config"
	"smith/internal/index"
	"smith/internal/store"
)

// tier0Tool registers a Tier 0 authored tool with a given added-at time.
func tier0Tool(t *testing.T, p config.Paths, name, version string, added time.Time) {
	t.Helper()
	dir := filepath.Join(store.QuarantineDir(p.StoreQuarantine, name, version), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, _ := store.HashFile(bin)
	e := index.Entry{
		Name: name, Version: version, Platform: index.PlatformAny, Tier: 0,
		Origin: "authored:test", Source: "local:/x", AddedAt: added.UTC().Format(time.RFC3339),
		Artifacts: []index.Artifact{{Path: filepath.Join("bin", name), Sha256: sum}},
	}
	if err := index.Save(p.IndexTools, e); err != nil {
		t.Fatal(err)
	}
}

func TestGCRemovesOldTier0Only(t *testing.T) {
	p := testPaths(t)
	tier0Tool(t, p, "old", "0.1.0", time.Now().Add(-60*24*time.Hour))
	tier0Tool(t, p, "fresh", "0.1.0", time.Now())

	// Dry run removes nothing.
	if err := cmdGC(p, []string{"--older-than", "30d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Load(p.IndexTools, "old", "0.1.0"); err != nil {
		t.Fatal("dry run deleted the entry")
	}
	// Apply.
	if err := cmdGC(p, []string{"--older-than", "30d", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Load(p.IndexTools, "old", "0.1.0"); err == nil {
		t.Fatal("old Tier 0 tool survived gc")
	}
	if _, err := os.Stat(store.QuarantineDir(p.StoreQuarantine, "old", "0.1.0")); !os.IsNotExist(err) {
		t.Fatal("old tool binary not removed")
	}
	if _, err := index.Load(p.IndexTools, "fresh", "0.1.0"); err != nil {
		t.Fatal("fresh Tier 0 tool was wrongly collected")
	}
}

func TestGCNeverTouchesTrusted(t *testing.T) {
	p := testPaths(t)
	// A promoted (Tier 1) tool that is ancient must never be GC'd.
	e := fakeTool(t, p, "keep", "1.0.0")
	if err := promoteEntry(p, e, "rev"); err != nil {
		// promoteEntry needs no transport for a Tier 2 fakeTool (source != local:)
		t.Fatal(err)
	}
	got, _ := index.Load(p.IndexTools, "keep", "1.0.0")
	got.AddedAt = time.Now().Add(-365 * 24 * time.Hour).Format(time.RFC3339)
	_ = index.Save(p.IndexTools, got)

	if err := cmdGC(p, []string{"--older-than", "1d", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Load(p.IndexTools, "keep", "1.0.0"); err != nil {
		t.Fatal("gc deleted a Tier 1 tool")
	}
}

func TestSyncRevokesDemotedUpstream(t *testing.T) {
	p := testPaths(t)
	// Install a Tier 1 tool locally (promoted), then simulate an upstream
	// demotion by rewriting only the index entry to Tier 2 — the binary is
	// still in the trusted store and on PATH, as it would be pre-sync.
	e := fakeTool(t, p, "widget", "1.0.0")
	if err := promoteEntry(p, e, "rev"); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(p.TrustedBin, "widget")
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("precondition: widget should be on PATH")
	}
	demoted, _ := index.Load(p.IndexTools, "widget", "1.0.0")
	demoted.Tier = 2
	if err := index.Save(p.IndexTools, demoted); err != nil {
		t.Fatal(err)
	}

	if err := cmdSync(p, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("sync did not remove the revoked tool from PATH")
	}
	if _, err := os.Stat(store.TrustedDir(p.StoreTrusted, "widget", "1.0.0")); !os.IsNotExist(err) {
		t.Fatal("revoked tool still in trusted store")
	}
	if _, err := os.Stat(store.QuarantineDir(p.StoreQuarantine, "widget", "1.0.0")); err != nil {
		t.Fatal("revoked tool not moved to quarantine")
	}
}
