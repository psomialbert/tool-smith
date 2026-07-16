package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/index"
	"smith/internal/store"
)

func TestSkillExportLifecycle(t *testing.T) {
	p := testPaths(t)
	skills := t.TempDir()
	if err := os.WriteFile(filepath.Join(p.Home, "config.yaml"),
		[]byte("skills:\n  dir: "+skills+"\n  export: always\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A promoted tool with a SKILL.md in its trusted dir.
	e := fakeTool(t, p, "widget", "1.0.0")
	if err := promoteEntry(p, e, "rev"); err != nil {
		t.Fatal(err)
	}
	tdir := store.TrustedDir(p.StoreTrusted, "widget", "1.0.0")
	if err := os.WriteFile(filepath.Join(tdir, "SKILL.md"), []byte("---\nname: widget\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promoted, _ := index.Load(p.IndexTools, "widget", "1.0.0")

	// export: always -> no prompt; skill lands in the skills dir.
	maybeExportSkill(p, bufio.NewReader(strings.NewReader("")), promoted)
	exported := filepath.Join(skills, "widget", "SKILL.md")
	if _, err := os.Stat(exported); err != nil {
		t.Fatalf("skill not exported: %v", err)
	}

	// demote removes the exported skill.
	removeExportedSkill(p, "widget")
	if _, err := os.Stat(exported); !os.IsNotExist(err) {
		t.Fatal("exported skill survived removal")
	}
}

func TestSkillCommandExportRemove(t *testing.T) {
	p := testPaths(t)
	skills := t.TempDir()
	os.WriteFile(filepath.Join(p.Home, "config.yaml"),
		[]byte("skills:\n  dir: "+skills+"\n"), 0o644)

	// Promote a tool and give it a SKILL.md in the trusted store.
	e := fakeTool(t, p, "widget", "1.0.0")
	if err := promoteEntry(p, e, "rev"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(store.TrustedDir(p.StoreTrusted, "widget", "1.0.0"), "SKILL.md"),
		[]byte("---\nname: widget\n---\n"), 0o644)

	// export (already-promoted tool, no re-promote needed)
	if err := cmdSkill(p, []string{"export", "widget"}); err != nil {
		t.Fatalf("skill export: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skills, "widget", "SKILL.md")); err != nil {
		t.Fatal("skill not exported")
	}

	// non-Tier-1 export is refused
	fakeTool(t, p, "ext", "2.0.0") // stays Tier 2
	if err := cmdSkill(p, []string{"export", "ext"}); err == nil {
		t.Fatal("exporting a non-Tier-1 tool should be refused")
	}

	// remove
	if err := cmdSkill(p, []string{"remove", "widget"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skills, "widget")); !os.IsNotExist(err) {
		t.Fatal("skill dir not removed")
	}
}

func TestSkillExportNeverAndNoSkill(t *testing.T) {
	p := testPaths(t)
	skills := t.TempDir()

	// export: never -> nothing exported even with a SKILL.md present.
	os.WriteFile(filepath.Join(p.Home, "config.yaml"),
		[]byte("skills:\n  dir: "+skills+"\n  export: never\n"), 0o644)
	e := fakeTool(t, p, "widget", "1.0.0")
	_ = promoteEntry(p, e, "rev")
	tdir := store.TrustedDir(p.StoreTrusted, "widget", "1.0.0")
	os.WriteFile(filepath.Join(tdir, "SKILL.md"), []byte("x"), 0o644)
	promoted, _ := index.Load(p.IndexTools, "widget", "1.0.0")
	maybeExportSkill(p, bufio.NewReader(strings.NewReader("")), promoted)
	if _, err := os.Stat(filepath.Join(skills, "widget", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("export:never still exported")
	}

	// A tool without a SKILL.md (external) is a silent no-op even on always.
	os.WriteFile(filepath.Join(p.Home, "config.yaml"),
		[]byte("skills:\n  dir: "+skills+"\n  export: always\n"), 0o644)
	e2 := fakeTool(t, p, "ext", "2.0.0")
	_ = promoteEntry(p, e2, "rev")
	// no SKILL.md written for ext
	promoted2, _ := index.Load(p.IndexTools, "ext", "2.0.0")
	maybeExportSkill(p, bufio.NewReader(strings.NewReader("")), promoted2)
	if _, err := os.Stat(filepath.Join(skills, "ext")); !os.IsNotExist(err) {
		t.Fatal("exported a skill for a tool without SKILL.md")
	}
}
