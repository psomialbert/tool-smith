package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/scaffold"
)

func gitRunner(t *testing.T, repo string) func(...string) {
	t.Helper()
	return func(args ...string) {
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// twoTagToolRepo scaffolds a bash tool and tags v0.1.0, then changes the
// entrypoint and tags v0.2.0 (so the vendored source + sha differ between them).
func twoTagToolRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "greet-repo")
	if err := scaffold.Create(repo, scaffold.Manifest{
		Name: "greet", Version: "0.1.0", Description: "greeter", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	git := gitRunner(t, repo)
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "v1")
	git("tag", "v0.1.0")
	entry := filepath.Join(repo, "greet")
	raw, _ := os.ReadFile(entry)
	if err := os.WriteFile(entry, append(raw, []byte("\n# v0.2.0 change\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "v2")
	git("tag", "v0.2.0")
	return repo
}

func captureStdout(t *testing.T, f func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := f()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("fn returned error: %v", err)
	}
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestSemverLess(t *testing.T) {
	less := [][2]string{
		{"v0.1.0", "v0.2.0"}, {"v0.2.0", "v0.10.0"}, {"v0.1.9", "v0.2.0"},
		{"1.0.0", "1.0.1"}, {"v1.2.3", "v2.0.0"},
	}
	for _, c := range less {
		if !semverLess(c[0], c[1]) {
			t.Errorf("expected %s < %s", c[0], c[1])
		}
		if semverLess(c[1], c[0]) {
			t.Errorf("expected NOT %s < %s", c[1], c[0])
		}
	}
	if semverLess("v1.0.0", "v1.0.0") {
		t.Error("equal versions must not be less")
	}
}

func TestLatestTagPicksHighestSemver(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := filepath.Join(t.TempDir(), "r")
	os.MkdirAll(repo, 0o755)
	os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644)
	git := gitRunner(t, repo)
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "c")
	for _, tag := range []string{"v0.1.0", "v0.2.0", "v0.10.0", "nightly"} {
		git("tag", tag)
	}
	got, err := latestTag(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.10.0" { // numeric, not lexical; non-semver "nightly" ignored
		t.Fatalf("want v0.10.0, got %q", got)
	}
}

// latestTag feeds the (hand-editable) lockfile source straight to git, so it
// must reject a remote-helper transport / option-injection source BEFORE the
// git call — the same gate `smith use` applies before cloning.
func TestLatestTagRejectsMaliciousSource(t *testing.T) {
	for _, src := range []string{"ext::sh -c touch${IFS}pwned", "fd::0", "-oProxyCommand=x"} {
		if _, err := latestTag(src); err == nil {
			t.Fatalf("latestTag must refuse dangerous source %q", src)
		}
	}
}

func TestUpgradeReAdoptsToLatest(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	p := testPaths(t)
	repo := twoTagToolRepo(t)
	t.Chdir(t.TempDir())

	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatalf("adopt v0.1.0: %v", err)
	}
	before, _ := loadProjLock()
	if before.Tools["greet"].Version != "v0.1.0" {
		t.Fatalf("expected v0.1.0 pin, got %+v", before.Tools["greet"])
	}
	if err := cmdUpgrade(p, nil); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	after, _ := loadProjLock()
	if after.Tools["greet"].Version != "v0.2.0" {
		t.Fatalf("expected v0.2.0 after upgrade, got %+v", after.Tools["greet"])
	}
	if after.Tools["greet"].Sha256 == before.Tools["greet"].Sha256 {
		t.Fatal("sha should change after upgrade (source differs)")
	}
}

func TestUpgradeUpToDateIsNoop(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	p := testPaths(t)
	repo := twoTagToolRepo(t)
	t.Chdir(t.TempDir())

	if err := cmdUse(p, []string{repo + "@v0.2.0"}); err != nil {
		t.Fatalf("adopt v0.2.0: %v", err)
	}
	before, _ := loadProjLock()
	if err := cmdUpgrade(p, nil); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	after, _ := loadProjLock()
	if before.Tools["greet"].Sha256 != after.Tools["greet"].Sha256 {
		t.Fatal("up-to-date upgrade must be a no-op")
	}
}

func TestUpgradeSelftestFailKeepsOldPin(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	p := testPaths(t)
	repo := filepath.Join(t.TempDir(), "greet-repo")
	if err := scaffold.Create(repo, scaffold.Manifest{
		Name: "greet", Version: "0.1.0", Description: "greeter", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	git := gitRunner(t, repo)
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "v1")
	git("tag", "v0.1.0")
	// v0.2.0 has a self-test that always fails.
	os.WriteFile(filepath.Join(repo, "selftest.sh"), []byte("#!/usr/bin/env bash\nexit 1\n"), 0o755)
	git("add", "-A")
	git("commit", "-qm", "v2")
	git("tag", "v0.2.0")
	t.Chdir(t.TempDir())

	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatalf("adopt v0.1.0: %v", err)
	}
	before, _ := loadProjLock()
	if err := cmdUpgrade(p, nil); err == nil {
		t.Fatal("upgrade must fail when the new version's self-test fails")
	}
	after, _ := loadProjLock()
	if after.Tools["greet"].Version != "v0.1.0" {
		t.Fatalf("old pin must be kept on failed upgrade, got %+v", after.Tools["greet"])
	}
	if after.Tools["greet"].Sha256 != before.Tools["greet"].Sha256 {
		t.Fatal("vendored pin changed despite a failed upgrade")
	}
}

func TestDiffShowsChangedFileJSON(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	p := testPaths(t)
	repo := twoTagToolRepo(t)
	t.Chdir(t.TempDir())

	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatalf("adopt v0.1.0: %v", err)
	}
	out := captureStdout(t, func() error { return cmdDiff(p, []string{"greet@v0.2.0", "--json"}) })
	if !strings.Contains(out, `"tool"`) || !strings.Contains(out, "greet") {
		t.Fatalf("diff json missing tool: %s", out)
	}
	if !strings.Contains(out, `"changes"`) || !strings.Contains(out, `"file"`) {
		t.Fatalf("diff json should list changed files (greet changed v0.1.0→v0.2.0): %s", out)
	}
}

func TestDiffNoChangesAtSameVersion(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	p := testPaths(t)
	repo := twoTagToolRepo(t)
	t.Chdir(t.TempDir())

	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatalf("adopt v0.1.0: %v", err)
	}
	// Diffing the pinned version against itself must show NO changes — guards
	// the mode-only false positive (vendored 0755 vs mirror 0644).
	out := captureStdout(t, func() error { return cmdDiff(p, []string{"greet@v0.1.0"}) })
	if !strings.Contains(out, "no source changes") {
		t.Fatalf("expected no changes at same version, got:\n%s", out)
	}
}

func TestDiffNumstatJSONParsing(t *testing.T) {
	out := captureStdout(t, func() error {
		return diffNumstatJSON("3\t1\ta/greet\n0\t0\tb/manifest.yaml\n", "greet", "v0.1.0", "v0.2.0")
	})
	for _, want := range []string{`"tool": "greet"`, `"from": "v0.1.0"`, `"to": "v0.2.0"`, `"added": 3`, `"greet"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("numstat json missing %q:\n%s", want, out)
		}
	}
}

// TestUpgradeRefusesRename guards the duplicate+orphan bug: if a tool renames
// itself in a newer version, `smith upgrade` must refuse (not vendor a second
// tool under the new name, orphan the old pin+dir, and report false success).
func TestUpgradeRefusesRename(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	p := testPaths(t)
	repo := filepath.Join(t.TempDir(), "greet-repo")
	if err := scaffold.Create(repo, scaffold.Manifest{
		Name: "greet", Version: "0.1.0", Description: "greeter", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	git := gitRunner(t, repo)
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "v1")
	git("tag", "v0.1.0")
	// v0.2.0 renames the tool.
	mf := filepath.Join(repo, "manifest.yaml")
	raw, _ := os.ReadFile(mf)
	os.WriteFile(mf, []byte(strings.Replace(string(raw), "name: greet", "name: greeter", 1)), 0o644)
	git("add", "-A")
	git("commit", "-qm", "v2")
	git("tag", "v0.2.0")

	proj := t.TempDir()
	t.Chdir(proj)
	if err := cmdUse(p, []string{repo + "@v0.1.0"}); err != nil {
		t.Fatalf("adopt v0.1.0: %v", err)
	}
	// Upgrade must report failure (not false success) — the per-tool reason is
	// the rename refusal; the aggregate error is enough to prove it didn't
	// silently "succeed".
	if err := cmdUpgrade(p, []string{"greet"}); err == nil {
		t.Fatal("upgrade of a renamed tool must fail, not report success")
	}
	// No phantom second tool, old pin intact.
	if _, statErr := os.Stat(filepath.Join(smithDir, "tools", "greeter")); statErr == nil {
		t.Fatal("upgrade vendored a second tool under the new name")
	}
	if lock, _ := loadProjLock(); lock.Tools["greet"].Version != "v0.1.0" || len(lock.Tools) != 1 {
		t.Fatalf("old pin should be untouched and sole entry, got %+v", lock.Tools)
	}
}
