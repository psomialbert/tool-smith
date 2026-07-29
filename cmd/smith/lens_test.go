package main

import (
	"os"
	"strings"
	"testing"
)

func TestLensMarkdownAndJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	seedBashTool(t, "greet") // .smith/tools/greet with a SKILL.md sharp trigger
	saveProjLock(ProjLock{Tools: map[string]ProjTool{
		"greet": {Source: "x/greet", Version: "v1", Runtime: "bash", Entrypoint: "greet"},
		"pup":   {Source: "mise:ubi:DataDog/pup", Version: "1.2.3", Runtime: "mise"},
	}})

	md := captureStdout(t, func() error { return cmdLens(testPaths(t), nil) })
	for _, want := range []string{"greet", "pup", "sharp trigger", "external (mise)", "greet --json"} {
		if !strings.Contains(md, want) {
			t.Fatalf("lens markdown missing %q:\n%s", want, md)
		}
	}

	js := captureStdout(t, func() error { return cmdLens(testPaths(t), []string{"--json"}) })
	for _, want := range []string{`"name"`, `"greet"`, `"external": true`, `"trigger"`} {
		if !strings.Contains(js, want) {
			t.Fatalf("lens json missing %q:\n%s", want, js)
		}
	}
	// An external mise CLI doesn't promise the `<tool> --json` contract, so its
	// invoke hint must not advertise it.
	if strings.Contains(js, "pup --json") {
		t.Fatalf("mise tool must not claim the --json contract:\n%s", js)
	}
}

func TestLensEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	out := captureStdout(t, func() error { return cmdLens(testPaths(t), nil) })
	if !strings.Contains(out, "No smith tools adopted") {
		t.Fatalf("empty project lens should say so, got: %s", out)
	}
}

func TestSkillDescription(t *testing.T) {
	// A prose trigger with ": " must survive the lenient line-scan.
	dir := t.TempDir()
	path := dir + "/SKILL.md"
	if err := os.WriteFile(path, []byte("---\nname: x\ndescription: Do a thing. Also `--parse` mode: score a blob.\n---\n# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := skillDescription(path)
	if !strings.Contains(got, "score a blob") {
		t.Fatalf("skillDescription dropped the trigger: %q", got)
	}
	if skillDescription(dir+"/nope.md") != "" {
		t.Fatal("missing file should yield empty description")
	}
}
