package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/config"
	"smith/internal/index"
	"smith/internal/sign"
	"smith/internal/store"
)

func writeTransportConfig(t *testing.T, p config.Paths, dir string) {
	t.Helper()
	cfg := "transport:\n  kind: local-dir\n  path: " + dir + "\n"
	if err := os.WriteFile(filepath.Join(p.Home, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// authoredTool registers a Tier 0 tool the way `smith register` would
// (machine-local source, platform any).
func authoredTool(t *testing.T, p config.Paths, name, version string) index.Entry {
	t.Helper()
	e := fakeTool(t, p, name, version)
	e.Tier = 0
	e.Origin = "authored:test"
	e.Source = "local:/drafts/" + name
	if err := index.Save(p.IndexTools, e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSignVerifyAndTamperDetection(t *testing.T) {
	p := testPaths(t)
	fakeTool(t, p, "a", "1.0.0")
	if _, err := sign.SignIndex(p.Home, p.IndexTools, nowUTC()); err != nil {
		t.Fatal(err)
	}
	present, err := sign.VerifyIndex(p.Home, p.IndexTools)
	if !present || err != nil {
		t.Fatalf("fresh signature should verify: present=%v err=%v", present, err)
	}
	// Tamper with an index entry after signing.
	path := filepath.Join(p.IndexTools, "a", "1.0.0", index.PlatformAny+".yaml")
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, append(raw, []byte("# sneaky\n")...), 0o644)
	if _, err := sign.VerifyIndex(p.Home, p.IndexTools); err == nil {
		t.Fatal("tampered manifest set verified clean")
	}
}

func TestPromoteAuthoredRefusedWithoutTransport(t *testing.T) {
	p := testPaths(t)
	e := authoredTool(t, p, "mytool", "0.1.0")
	err := promoteEntry(p, e, "reviewer")
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("expected transport refusal, got: %v", err)
	}
	// Not moved, not re-tiered.
	if _, statErr := os.Stat(store.QuarantineDir(p.StoreQuarantine, "mytool", "0.1.0")); statErr != nil {
		t.Fatal("tool left quarantine despite refused promotion")
	}
	got, _ := index.Load(p.IndexTools, "mytool", "0.1.0")
	if got.Tier != 0 {
		t.Fatalf("tier changed to %d despite refused promotion", got.Tier)
	}
}

func TestPromotePushesBlobThenMachineBFetches(t *testing.T) {
	shared := t.TempDir() // the "synced folder" between machines

	// --- machine A: author, promote (blob push), sign ---
	pA := testPaths(t)
	writeTransportConfig(t, pA, shared)
	e := authoredTool(t, pA, "mytool", "0.1.0")
	if err := promoteEntry(pA, e, "albert"); err != nil {
		t.Fatalf("promote with transport: %v", err)
	}
	got, err := index.Load(pA.IndexTools, "mytool", "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Blob == nil || strings.HasPrefix(got.Source, "local:") {
		t.Fatalf("promotion did not rewrite source to shared transport: %+v", got)
	}
	if _, err := sign.SignIndex(pA.Home, pA.IndexTools, nowUTC()); err != nil {
		t.Fatal(err)
	}

	// --- machine B: index + pubkey + sig arrive via git sync; store does not ---
	pB := testPaths(t)
	writeTransportConfig(t, pB, shared)
	if err := os.CopyFS(filepath.Join(pB.Home, "index"), os.DirFS(filepath.Join(pA.Home, "index"))); err != nil {
		t.Fatal(err)
	}
	if err := cmdFetch(pB, []string{"mytool@0.1.0"}); err != nil {
		t.Fatalf("fetch on machine B: %v", err)
	}
	link := filepath.Join(pB.TrustedBin, "mytool")
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("fetched Tier 1 tool not linked on PATH:", err)
	}
	bin := filepath.Join(store.TrustedDir(pB.StoreTrusted, "mytool", "0.1.0"), "bin", "mytool")
	sumB, err := store.HashFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if sumB != got.Artifacts[0].Sha256 {
		t.Fatal("machine B binary hash differs from pinned")
	}
}

func TestFetchQuarantinesWhenIndexUnsigned(t *testing.T) {
	shared := t.TempDir()
	pA := testPaths(t)
	writeTransportConfig(t, pA, shared)
	e := authoredTool(t, pA, "mytool", "0.1.0")
	if err := promoteEntry(pA, e, "albert"); err != nil {
		t.Fatal(err)
	}
	// No SignIndex: machine B gets an unsigned index.
	pB := testPaths(t)
	writeTransportConfig(t, pB, shared)
	if err := os.CopyFS(filepath.Join(pB.Home, "index"), os.DirFS(filepath.Join(pA.Home, "index"))); err != nil {
		t.Fatal(err)
	}
	if err := cmdFetch(pB, []string{"mytool@0.1.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(pB.TrustedBin, "mytool")); err == nil {
		t.Fatal("unsigned index but tool landed on PATH")
	}
	if _, err := os.Stat(store.QuarantineDir(pB.StoreQuarantine, "mytool", "0.1.0")); err != nil {
		t.Fatal("tool not in quarantine:", err)
	}
	got, _ := index.Load(pB.IndexTools, "mytool", "0.1.0")
	if got.Tier != 2 {
		t.Fatalf("local tier = %d, want 2 (quarantined)", got.Tier)
	}
}
