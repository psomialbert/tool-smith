package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeToolTree lays out a tool repo under a temp dir and returns its path.
func writeToolTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// THE regression test for this feature. A tool that declares no `sources:` must
// hash byte-for-byte as it always has — its file list feeds bundleHash, so any
// change to it changes every already-adopted tool's pin and makes `smith
// install` / `smith verify` refuse every tool adopted anywhere. Pinned to a
// literal digest rather than a recomputation, so a change to the file SET is
// caught and not silently mirrored by the assertion.
func TestLegacyBundleHashUnchanged(t *testing.T) {
	dir := writeToolTree(t, map[string]string{
		"greet":         "#!/bin/sh\necho hi\n",
		"manifest.yaml": "name: greet\nversion: 0.1.0\nruntime: bash\nentrypoint: greet\n",
		"selftest.sh":   "#!/bin/sh\nexit 0\n",
		"SKILL.md":      "---\nname: greet\ndescription: d\n---\n",
		// Present in every scaffolded tool and deliberately NOT part of the
		// legacy bundle. If a tree walk ever replaces the explicit list, this
		// file joins the hash and the digest below changes.
		"README.md": "docs that must stay out of the hash\n",
	})

	src, err := toolSourceFiles(dir, "bash", "greet", "greet", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := bundleHash(dir, src)
	if err != nil {
		t.Fatal(err)
	}

	// Pinned literal, computed over the fixture above. Deliberately NOT
	// recomputed from sourceFiles() here: an assertion that recomputes its own
	// expectation moves with the code and would happily accept a changed file
	// set, which is the exact regression this test exists to catch.
	const want = "145c5ee28c06955537246e2da40c1fdc5785c16f522651d6dc94bfc8895d284f"
	if got != want {
		t.Fatalf("legacy bundle hash changed: got %s want %s\nfile set was %v\n\n"+
			"If you changed the legacy file SET, stop: every already-adopted tool's "+
			"pin breaks and `smith install`/`smith verify` will refuse them all. "+
			"If you deliberately changed the hash FORMAT, that is a breaking change "+
			"needing a migration, not a new digest here.", got, want, src)
	}
}

func TestResolveSourcesExpandsDirsSortedWithMeta(t *testing.T) {
	dir := writeToolTree(t, map[string]string{
		"main.go":           "package main\n",
		"internal/b.go":     "package main\n",
		"internal/a.go":     "package main\n",
		"internal/sub/c.go": "package main\n",
		"manifest.yaml":     "x\n",
		"selftest.sh":       "x\n",
		"SKILL.md":          "x\n",
		"README.md":         "not listed\n",
	})
	got, err := resolveSources(dir, []string{"main.go", "internal"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"SKILL.md", "internal/a.go", "internal/b.go", "internal/sub/c.go",
		"main.go", "manifest.yaml", "selftest.sh",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	for _, g := range got {
		if g == "README.md" {
			t.Error("unlisted README.md was included; sources must be explicit")
		}
	}
}

func TestResolveSourcesRejectsEscapesAndJunk(t *testing.T) {
	dir := writeToolTree(t, map[string]string{
		"main.go": "package main\n", "manifest.yaml": "x\n", "selftest.sh": "x\n", "SKILL.md": "x\n",
	})
	cases := []struct{ name, entry, wantMsg string }{
		{"parent traversal", "../secrets", "escapes"},
		{"nested traversal", "internal/../../secrets", "escapes"},
		{"absolute", "/etc/passwd", "absolute"},
		{"whole repo", ".", "not allowed"},
		{"empty", "  ", "empty"},
		{"missing", "nope.go", "does not exist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolveSources(dir, []string{c.entry})
			if err == nil {
				t.Fatalf("entry %q was accepted", c.entry)
			}
			if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("error should mention %q, got: %v", c.wantMsg, err)
			}
		})
	}
}

// A symlink inside a tool repo can point outside it, and the vendor copy reads
// through it — that would pull arbitrary host files into the project, outside
// anything the author declared.
func TestResolveSourcesRefusesSymlinks(t *testing.T) {
	dir := writeToolTree(t, map[string]string{
		"main.go": "package main\n", "manifest.yaml": "x\n", "selftest.sh": "x\n", "SKILL.md": "x\n",
		"internal/real.go": "package main\n",
	})
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	// As a directly listed entry.
	if err := os.Symlink(secret, filepath.Join(dir, "leak.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := resolveSources(dir, []string{"leak.go"}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("listed symlink must be refused, got: %v", err)
	}

	// And nested inside a listed directory, where it's easier to miss.
	if err := os.Symlink(secret, filepath.Join(dir, "internal", "leak.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSources(dir, []string{"internal"}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlink nested under a listed dir must be refused, got: %v", err)
	}
}

func TestResolveSourcesEnforcesCaps(t *testing.T) {
	files := map[string]string{"manifest.yaml": "x\n", "selftest.sh": "x\n", "SKILL.md": "x\n"}
	for i := 0; i < maxSourceFiles+5; i++ {
		files["pkg/f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go"] = "package main\n"
	}
	dir := writeToolTree(t, files)
	_, err := resolveSources(dir, []string{"pkg"})
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("file-count cap not enforced, got: %v", err)
	}

	big := writeToolTree(t, map[string]string{
		"manifest.yaml": "x\n", "selftest.sh": "x\n", "SKILL.md": "x\n",
		"huge.go": strings.Repeat("x", maxSourceBytes+1),
	})
	_, err = resolveSources(big, []string{"huge.go"})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("byte cap not enforced, got: %v", err)
	}
}

func TestResolveSourcesRefusesEmptyResult(t *testing.T) {
	dir := writeToolTree(t, map[string]string{
		"manifest.yaml": "x\n", "selftest.sh": "x\n", "SKILL.md": "x\n", "empty/.keep": "",
	})
	// A listed directory containing no regular files resolves to nothing.
	if err := os.Remove(filepath.Join(dir, "empty", ".keep")); err != nil {
		t.Fatal(err)
	}
	_, err := resolveSources(dir, []string{"empty"})
	if err == nil || !strings.Contains(err.Error(), "no files") {
		t.Fatalf("empty resolution must be refused, got: %v", err)
	}
}
