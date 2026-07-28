package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/scaffold"
)

func TestExpandSource(t *testing.T) {
	cases := map[string]string{
		"github:acme/greet":         "https://github.com/acme/greet",
		"acme/greet":                "https://github.com/acme/greet",
		"https://example.com/x.git": "https://example.com/x.git",
		"git@github.com:acme/x.git": "git@github.com:acme/x.git",
		"/abs/path/repo":            "/abs/path/repo",
		"./rel/repo":                "./rel/repo",
	}
	for in, want := range cases {
		if got := expandSource(in); got != want {
			t.Errorf("expandSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBundleHashDeterministicAndSensitive(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "greet"), []byte("#!/bin/sh\necho hi\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("name: greet\n"), 0o644)
	files := []string{"greet", "manifest.yaml"}
	h1, err := bundleHash(dir, files)
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := bundleHash(dir, files)
	if h1 != h2 {
		t.Fatal("bundleHash not deterministic")
	}
	os.WriteFile(filepath.Join(dir, "greet"), []byte("#!/bin/sh\necho evil\n"), 0o755)
	h3, _ := bundleHash(dir, files)
	if h3 == h1 {
		t.Fatal("bundleHash did not change when a file changed")
	}
}

func TestProjLockRoundtrip(t *testing.T) {
	t.Chdir(t.TempDir())
	in := ProjLock{Tools: map[string]ProjTool{
		"greet": {Source: "acme/greet", Version: "v1", Sha256: "abc", Entrypoint: "greet", Runtime: "bash"},
	}}
	if err := saveProjLock(in); err != nil {
		t.Fatal(err)
	}
	out, err := loadProjLock()
	if err != nil {
		t.Fatal(err)
	}
	if out.Tools["greet"] != in.Tools["greet"] {
		t.Fatalf("roundtrip mismatch: %+v", out.Tools["greet"])
	}
	// Missing lockfile -> empty, no error.
	t.Chdir(t.TempDir())
	if l, err := loadProjLock(); err != nil || len(l.Tools) != 0 {
		t.Fatalf("empty project: %v %+v", err, l.Tools)
	}
}

// Go tools: cmdUse vendors main.go (source, reviewable), cmdInstall builds it.
func TestUseInstallGoTool(t *testing.T) {
	requireSandbox(t)
	for _, b := range []string{"git", "go"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not available", b)
		}
	}
	p := testPaths(t)
	repo := filepath.Join(t.TempDir(), "hasher-repo")
	if err := scaffold.Create(repo, scaffold.Manifest{
		Name: "hasher", Version: "0.1.0", Description: "go tool", Runtime: "go",
	}); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo, "v0.1.0")

	proj := t.TempDir()
	t.Chdir(proj)
	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatalf("smith use (go): %v", err)
	}
	// Vendored source is main.go, NOT a compiled binary.
	if _, err := os.Stat(filepath.Join(smithDir, "tools", "hasher", "main.go")); err != nil {
		t.Fatal("go source main.go not vendored")
	}
	if _, err := os.Stat(filepath.Join(smithDir, "tools", "hasher", "hasher")); err == nil {
		t.Fatal("a built binary was vendored (should be source-only)")
	}
	if lock, _ := loadProjLock(); lock.Tools["hasher"].Runtime != "go" {
		t.Fatal("lockfile runtime not go")
	}
	// Install builds a real binary onto the project bin (not a symlink).
	if err := cmdInstall(p, nil); err != nil {
		t.Fatalf("smith install (go): %v", err)
	}
	fi, err := os.Lstat(filepath.Join(smithDir, "bin", "hasher"))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&0o111 == 0 {
		t.Fatalf("expected a built executable at .smith/bin/hasher, got %v", fi.Mode())
	}
}

func gitInit(t *testing.T, repo, tag string) {
	t.Helper()
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-qm", "tool")
	run("tag", tag)
}

// python-uv tools: cmdUse locks + warms + vendors source (.py + .py.lock),
// cmdInstall warms a cache + writes a launcher. Depless (no PyPI needed);
// skips if uv can't resolve offline (no network for the python resolve).
func TestUseInstallPythonUV(t *testing.T) {
	requireSandbox(t)
	for _, b := range []string{"git", "uv"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not available", b)
		}
	}
	p := testPaths(t)
	repo := filepath.Join(t.TempDir(), "puv-repo")
	if err := scaffold.Create(repo, scaffold.Manifest{
		Name: "puv", Version: "0.1.0", Description: "python-uv tool", Runtime: "python-uv",
	}); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo, "v0.1.0")

	proj := t.TempDir()
	t.Chdir(proj)
	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Skipf("python-uv use needs uv to resolve (network?): %v", err)
	}
	for _, f := range []string{"puv.py", "puv.py.lock"} { // source + hash-lock vendored
		if _, err := os.Stat(filepath.Join(smithDir, "tools", "puv", f)); err != nil {
			t.Fatalf("expected vendored %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(smithDir, "tools", "puv", "uv-cache")); err == nil {
		t.Fatal("cache was vendored (should be source-only)")
	}
	if err := cmdInstall(p, nil); err != nil {
		t.Fatalf("smith install (python-uv): %v", err)
	}
	// Launcher is a regular executable (not a symlink), cache is git-ignored.
	fi, err := os.Lstat(filepath.Join(smithDir, "bin", "puv"))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a launcher file at .smith/bin/puv: %v", err)
	}
	if _, err := os.Stat(filepath.Join(smithDir, "cache", "puv")); err != nil {
		t.Fatal("warmed cache dir missing")
	}
}

// End-to-end through cmdUse + cmdInstall against a local git tool repo.
func TestUseInstallLocalRepo(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	p := testPaths(t)

	// Build a tool repo: scaffold greet, add a real self-test assertion, git it.
	repo := filepath.Join(t.TempDir(), "greet-repo")
	if err := scaffold.Create(repo, scaffold.Manifest{
		Name: "greet", Version: "0.1.0", Description: "greeter", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "greet")
	git("tag", "v0.1.0")

	// Adopt into a fresh project dir.
	proj := t.TempDir()
	t.Chdir(proj)
	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatalf("smith use: %v", err)
	}
	// Vendored source + lockfile present.
	if _, err := os.Stat(filepath.Join(smithDir, "tools", "greet", "greet")); err != nil {
		t.Fatal("vendored entrypoint missing")
	}
	lock, _ := loadProjLock()
	tg, ok := lock.Tools["greet"]
	if !ok || tg.Version != "v0.1.0" || tg.Runtime != "bash" || tg.Sha256 == "" {
		t.Fatalf("lockfile entry wrong: %+v", tg)
	}

	// Install -> project bin symlink.
	if err := cmdInstall(p, nil); err != nil {
		t.Fatalf("smith install: %v", err)
	}
	link := filepath.Join(smithDir, "bin", "greet")
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected bin symlink: %v", err)
	}

	// Tampering the vendored source makes install refuse (integrity gate).
	os.WriteFile(filepath.Join(smithDir, "tools", "greet", "greet"),
		[]byte("#!/bin/bash\necho tampered\n"), 0o755)
	if err := cmdInstall(p, nil); err == nil {
		t.Fatal("install should refuse a vendored-source sha mismatch")
	}
}

// TestInstallRejectsUnknownArgs guards against `smith install` silently ignoring
// flags/arguments (e.g. `smith install --json` or `smith install greet`) and
// running a full install anyway. Unknown args must be rejected before any work.
func TestInstallRejectsUnknownArgs(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	for _, a := range [][]string{{"--json"}, {"greet"}, {"--frozen"}} {
		err := cmdInstall(p, a)
		if err == nil || !strings.Contains(err.Error(), "unknown argument") {
			t.Errorf("cmdInstall(%v) = %v; want an 'unknown argument' error", a, err)
		}
	}
}

// TestWriteFileAtomic guards the atomic-write helper used for config.yaml and
// tools.yaml: it round-trips content, overwrites an existing file, applies the
// mode, and leaves no partial/temp sibling behind (a crash between write and
// rename can't yield a torn file).
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new-content"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new-content" {
		t.Fatalf("content = %q, want new-content", got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected only the target file, found stray temp files: %v", entries)
	}
}
