package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/index"
	"smith/internal/reviewtui"
	"smith/internal/scaffold"
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

// Full review wiring minus the raw terminal: load a real quarantine bundle,
// detect languages, drive the reviewer with scripted keys, and check the
// emitted report. Only term.MakeRaw in RunTerminal is left unexercised.
func TestReviewTUIWiringProducesReport(t *testing.T) {
	requireSandbox(t)
	p := testPaths(t)
	dir := draftDir(p, "demo")
	if err := scaffold.Create(dir, scaffold.Manifest{
		Name: "demo", Version: "0.1.0", Description: "demo", Runtime: "python",
	}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRegister(p, []string{"demo"}); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(p.StoreQuarantine, "demo", "0.1.0")
	all, _ := bundleFiles(base)
	e, _ := index.Load(p.IndexTools, "demo", "0.1.0")
	ordered := orderForReview(e, all)

	rf, err := loadReviewFiles(base, ordered)
	if err != nil {
		t.Fatal(err)
	}
	// First file should be the python entrypoint (source-first ordering).
	if rf[0].Name != "bin/demo" || rf[0].Lang != "python" {
		t.Fatalf("first review file = %s (%s), want bin/demo (python)", rf[0].Name, rf[0].Lang)
	}
	m := reviewtui.New("demo", "0.1.0", rf)
	var out bytes.Buffer
	// comment line 1 of the entrypoint: c, type, Enter, q
	keys := "c" + "check the shebang" + "\r" + "q"
	if err := reviewtui.Run(m, strings.NewReader(keys), &out, 12, 80, reviewtui.ThemeByName("mono")); err != nil {
		t.Fatal(err)
	}
	rep := m.Report()
	if !strings.Contains(rep, "demo@0.1.0") || !strings.Contains(rep, "bin/demo:1") || !strings.Contains(rep, "check the shebang") {
		t.Fatalf("report missing expected content:\n%s", rep)
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
