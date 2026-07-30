package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/scaffold"
)

// makeMultiFileGoRepo builds a tool repo whose entrypoint genuinely depends on a
// second file in a subdirectory — so if the vendor copy, the hash, or the package
// build only handled one file, the tool would not compile or would not run.
func makeMultiFileGoRepo(t *testing.T, dir string) {
	t.Helper()
	if err := scaffold.Create(dir, scaffold.Manifest{
		Name: "adder", Version: "0.1.0", Description: "sums two ints", Runtime: "go",
	}); err != nil {
		t.Fatal(err)
	}
	main := `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--json" {
		fmt.Printf("{\"sum\":%d}\n", Add(2, 3))
		return
	}
	fmt.Println(Add(2, 3))
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	// The second file. Same package, a subdirectory is NOT possible for a Go
	// package, so this sits alongside — the point is that it's a file the legacy
	// single-file list would never vendor.
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package main\n\n// Add is the whole reason this tool needs two files.\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A nested non-Go source file, to prove subdirectories survive the copy.
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "notes.txt"), []byte("nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A package build needs a main module. go.mod is auto-included in the bundle
	// (it decides the module path and Go version, so it must be hashed).
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module adder\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Declare the multi-file layout.
	m, err := scaffold.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Sources = []string{"main.go", "add.go", "data"}
	if err := scaffold.SaveManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	// Self-test must pass offline. smith invokes it as `selftest.sh <tool>`, so
	// the binary under test arrives as $1 — not in the environment.
	st := "#!/usr/bin/env bash\nset -euo pipefail\nTOOL=\"$1\"\n" +
		"fail() { echo \"SELFTEST FAIL: $*\" >&2; exit 1; }\n" +
		"[[ \"$(\"$TOOL\" --json)\" == '{\"sum\":5}' ]] || fail \"wrong sum — Add() came from the second file\"\n" +
		"echo \"selftest: ok\"\n"
	if err := os.WriteFile(filepath.Join(dir, "selftest.sh"), []byte(st), 0o755); err != nil {
		t.Fatal(err)
	}
}

// End to end: a multi-file Go tool adopts, vendors every declared file (including
// a nested one), builds as a package, and runs.
func TestUseInstallMultiFileGoTool(t *testing.T) {
	requireSandbox(t)
	for _, b := range []string{"git", "go"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not available", b)
		}
	}
	p := testPaths(t)
	repo := filepath.Join(t.TempDir(), "adder-repo")
	makeMultiFileGoRepo(t, repo)
	gitInit(t, repo, "v0.1.0")

	proj := t.TempDir()
	t.Chdir(proj)
	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatalf("smith use (multi-file go): %v", err)
	}

	vend := filepath.Join(smithDir, "tools", "adder")
	for _, want := range []string{"main.go", "add.go", "data/notes.txt", "manifest.yaml", "selftest.sh", "SKILL.md"} {
		if _, err := os.Stat(filepath.Join(vend, filepath.FromSlash(want))); err != nil {
			t.Errorf("declared source %q was not vendored: %v", want, err)
		}
	}
	// Still source-only: no built artifact vendored.
	if _, err := os.Stat(filepath.Join(vend, "adder")); err == nil {
		t.Error("a built binary was vendored (should be source-only)")
	}

	if err := cmdInstall(p, nil); err != nil {
		t.Fatalf("smith install (multi-file go): %v", err)
	}
	out, err := exec.Command(filepath.Join(smithDir, "bin", "adder"), "--json").Output()
	if err != nil {
		t.Fatalf("running the installed multi-file tool: %v", err)
	}
	if strings.TrimSpace(string(out)) != `{"sum":5}` {
		t.Errorf("multi-file tool output = %q; want {\"sum\":5} (Add came from the second file)", out)
	}
}

// Integrity must cover every file, not just the entrypoint: tampering with the
// NESTED vendored file has to be refused.
func TestMultiFileTamperedNestedFileRefused(t *testing.T) {
	requireSandbox(t)
	for _, b := range []string{"git", "go"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not available", b)
		}
	}
	p := testPaths(t)
	repo := filepath.Join(t.TempDir(), "adder-repo")
	makeMultiFileGoRepo(t, repo)
	gitInit(t, repo, "v0.1.0")

	proj := t.TempDir()
	t.Chdir(proj)
	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(smithDir, "tools", "adder", "data", "notes.txt")
	if err := os.WriteFile(nested, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdInstall(p, nil); err == nil {
		t.Fatal("install accepted a tampered NESTED vendored file")
	}
}

// The `sources:` list lives in the vendored manifest, which is itself inside the
// hashed bundle — so editing it to shrink the reviewed surface must be caught.
func TestMultiFileTamperedSourcesListRefused(t *testing.T) {
	requireSandbox(t)
	for _, b := range []string{"git", "go"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not available", b)
		}
	}
	p := testPaths(t)
	repo := filepath.Join(t.TempDir(), "adder-repo")
	makeMultiFileGoRepo(t, repo)
	gitInit(t, repo, "v0.1.0")

	proj := t.TempDir()
	t.Chdir(proj)
	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatal(err)
	}
	vend := filepath.Join(smithDir, "tools", "adder")
	m, err := scaffold.LoadManifest(vend)
	if err != nil {
		t.Fatal(err)
	}
	m.Sources = []string{"main.go"} // drop add.go + data from the reviewed set
	if err := scaffold.SaveManifest(vend, m); err != nil {
		t.Fatal(err)
	}
	if err := cmdInstall(p, nil); err == nil {
		t.Fatal("install accepted a vendored manifest whose sources: list was edited")
	}
}

// Multi-file must not become multi-dependency by accident: GOPROXY=off means a
// tool reaching for a third-party module fails closed at adopt time rather than
// fetching code before anyone reviewed it.
func TestMultiFileGoToolWithExternalDepFailsClosed(t *testing.T) {
	requireSandbox(t)
	for _, b := range []string{"git", "go"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not available", b)
		}
	}
	p := testPaths(t)
	repo := filepath.Join(t.TempDir(), "deps-repo")
	makeMultiFileGoRepo(t, repo)
	// Rewrite add.go to import something that isn't in the stdlib. The go.mod
	// from makeMultiFileGoRepo stays, so this fails because GOPROXY=off refuses
	// to fetch — NOT merely because a module is missing.
	if err := os.WriteFile(filepath.Join(repo, "add.go"),
		[]byte("package main\n\nimport \"github.com/nonexistent/pkg\"\n\nfunc Add(a, b int) int { return pkg.Sum(a, b) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo, "v0.1.0")

	proj := t.TempDir()
	t.Chdir(proj)
	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err == nil {
		t.Fatal("adopting a Go tool with a third-party import should fail closed (GOPROXY=off)")
	}
}
