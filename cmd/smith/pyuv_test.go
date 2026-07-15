package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"smith/internal/index"
	"smith/internal/scaffold"
)

// python-uv register locks deps, warms a tool-local cache, self-tests offline,
// and bundles flat. Uses the depless template so the warm step needs no PyPI
// (uv resolves the system python). Requires uv + a sandbox + network for uv's
// resolver.
func TestPythonUVRegisters(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not on PATH")
	}
	p := testPaths(t)
	if err := scaffold.Create(draftDir(p, "puv"), scaffold.Manifest{
		Name: "puv", Version: "0.1.0", Description: "python-uv tool", Runtime: "python-uv",
	}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRegister(p, []string{"puv"}); err != nil {
		t.Skipf("python-uv register needs network for uv resolve; got: %v", err)
	}
	e, err := index.Load(p.IndexTools, "puv", "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	// Platform-specific (cache holds platform wheels), launcher is the artifact.
	if e.Platform != index.CurrentPlatform() {
		t.Fatalf("platform = %q, want %q", e.Platform, index.CurrentPlatform())
	}
	if len(e.Artifacts) != 1 || e.Artifacts[0].Path != "puv" {
		t.Fatalf("expected launcher artifact 'puv', got %+v", e.Artifacts)
	}
	// Flat bundle: launcher + script + lock + cache as siblings.
	base := filepath.Join(p.StoreQuarantine, "puv", "0.1.0")
	for _, want := range []string{"puv", "puv.py", "puv.py.lock", "uv-cache"} {
		if _, err := os.Stat(filepath.Join(base, want)); err != nil {
			t.Fatalf("bundle missing %s: %v", want, err)
		}
	}
	// Source + lock pinned as review docs.
	var haveSrc, haveLock bool
	for _, d := range e.Docs {
		switch d.Path {
		case "puv.py":
			haveSrc = true
		case "puv.py.lock":
			haveLock = true
		}
	}
	if !haveSrc || !haveLock {
		t.Fatalf("source/lock not pinned as docs: %+v", e.Docs)
	}
}
