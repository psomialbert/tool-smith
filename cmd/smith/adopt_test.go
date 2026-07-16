package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
	h1, err := bundleHash(dir, "greet")
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := bundleHash(dir, "greet")
	if h1 != h2 {
		t.Fatal("bundleHash not deterministic")
	}
	os.WriteFile(filepath.Join(dir, "greet"), []byte("#!/bin/sh\necho evil\n"), 0o755)
	h3, _ := bundleHash(dir, "greet")
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
