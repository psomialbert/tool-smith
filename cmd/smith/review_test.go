package main

import (
	"path/filepath"
	"testing"

	"smith/internal/index"
)

func TestReviewHelpers(t *testing.T) {
	p := testPaths(t)
	fakeTool(t, p, "widget", "1.0.0") // Tier 2 quarantine with one artifact + provenance

	base := filepath.Join(p.StoreQuarantine, "widget", "1.0.0")
	files, err := bundleFiles(base)
	if err != nil || len(files) == 0 {
		t.Fatalf("bundleFiles: %v (%d files)", err, len(files))
	}

	if !isBinary([]byte{'a', 0, 'b'}) || isBinary([]byte("plain text\n")) {
		t.Fatal("isBinary misclassified")
	}

	entries := provenanceFor(p, "widget", "1.0.0")
	if len(entries) == 0 || entries[0].Tool != "widget" {
		t.Fatalf("provenanceFor returned %d entries", len(entries))
	}
}

func TestOrderForReviewSourceFirst(t *testing.T) {
	e := index.Entry{
		Artifacts: []index.Artifact{{Path: "bin/calc"}},
		Docs: []index.Artifact{
			{Path: "SKILL.md"}, {Path: "manifest.yaml"}, {Path: "bin/selftest.sh"},
		},
	}
	// Deliberately shuffled input (as WalkDir alphabetical would give).
	got := orderForReview(e, []string{"SKILL.md", "bin/calc", "bin/selftest.sh", "manifest.yaml"})
	want := []string{"bin/calc", "bin/selftest.sh", "SKILL.md", "manifest.yaml"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestReviewRunsAndOpenEditorFallsBack(t *testing.T) {
	p := testPaths(t)
	fakeTool(t, p, "widget", "1.0.0")

	// --paths mode must succeed read-only and leave the tier unchanged.
	if err := cmdReview(p, []string{"widget", "--paths"}); err != nil {
		t.Fatalf("review --paths: %v", err)
	}
	if e, _ := index.Load(p.IndexTools, "widget", "1.0.0"); e.Tier != 2 {
		t.Fatalf("review changed tier to %d", e.Tier)
	}

	// Empty editor: openInEditor prints paths and returns nil, never guesses.
	if err := openInEditor("", filepath.Join(p.StoreQuarantine, "widget", "1.0.0"),
		[]string{"bin/widget"}); err != nil {
		t.Fatalf("openInEditor with no editor should be a no-op, got %v", err)
	}
	// Blank/whitespace editor is treated as unset, not exec'd.
	if err := openInEditor("   ", filepath.Join(p.StoreQuarantine, "widget", "1.0.0"),
		[]string{"bin/widget"}); err != nil {
		t.Fatalf("openInEditor with whitespace editor should be a no-op, got %v", err)
	}
}
