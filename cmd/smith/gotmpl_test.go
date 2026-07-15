package main

import (
	"os/exec"
	"testing"

	"smith/internal/index"
	"smith/internal/scaffold"
)

func TestGoTemplateRegisters(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	p := testPaths(t)
	if err := scaffold.Create(draftDir(p, "hasher"), scaffold.Manifest{
		Name: "hasher", Version: "0.1.0", Description: "checksum", Runtime: "go",
	}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRegister(p, []string{"hasher"}); err != nil {
		t.Fatalf("register go tool: %v", err)
	}
	e, err := index.Load(p.IndexTools, "hasher", "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	// Compiled binary is platform-specific, not "any".
	if e.Platform != index.CurrentPlatform() {
		t.Fatalf("go tool platform = %q, want %q", e.Platform, index.CurrentPlatform())
	}
	if len(e.Artifacts) != 1 || e.Artifacts[0].Path != "bin/hasher" {
		t.Fatalf("expected bin/hasher artifact, got %+v", e.Artifacts)
	}
	// main.go pinned as a source doc for review.
	var hasSource bool
	for _, d := range e.Docs {
		if d.Path == "main.go" {
			hasSource = true
		}
	}
	if !hasSource {
		t.Fatalf("go source main.go not pinned as a doc: %+v", e.Docs)
	}
}
