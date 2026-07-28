package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/scaffold"
)

func TestGitHubSlugFromURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/greet.git":       "acme/greet",
		"https://github.com/acme/greet.git":   "acme/greet",
		"https://github.com/acme/greet":       "acme/greet",
		"ssh://git@github.com/acme/greet.git": "acme/greet",
		"git@mygithub.company.com:acme/x.git": "", // look-alike host: not github.com
		"https://github.company.com/acme/x":   "", // "github.com" not at a host boundary
		"https://gitlab.com/acme/greet.git":   "",
	}
	for url, want := range cases {
		if got := gitHubSlugFromURL(url); got != want {
			t.Errorf("gitHubSlugFromURL(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestPublishValidate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "greet")
	if err := scaffold.Create(dir, scaffold.Manifest{
		Name: "greet", Version: "0.1.0", Description: "g", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := publishValidate(dir); err != nil {
		t.Fatalf("scaffolded tool should validate: %v", err)
	}
	// Missing SKILL.md → invalid.
	os.Remove(filepath.Join(dir, "SKILL.md"))
	if _, err := publishValidate(dir); err == nil {
		t.Fatal("missing SKILL.md must fail validation")
	}
	// Not a tool dir at all → invalid.
	if _, err := publishValidate(t.TempDir()); err == nil {
		t.Fatal("a non-tool dir must fail validation")
	}
}

func TestPublishTag(t *testing.T) {
	cases := []struct{ ver, override, want string }{
		{"0.2.0", "", "v0.2.0"},
		{"v0.3.1", "", "v0.3.1"}, // leading v kept, not doubled
		{"0.1.0", "v9.9.9", "v9.9.9"},
	}
	for _, c := range cases {
		got := publishTag(scaffold.Manifest{Version: c.ver}, c.override)
		if got != c.want {
			t.Errorf("publishTag(%q, %q) = %q, want %q", c.ver, c.override, got, c.want)
		}
	}
}

// End-to-end without gh: push to a local bare "origin" so the remote-create
// (gh) path is skipped and we exercise commit + push + tag.
func TestPublishToLocalRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// Deterministic committer identity (CI may have none).
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	p := testPaths(t)

	bare := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	tool := filepath.Join(t.TempDir(), "greet")
	if err := scaffold.Create(tool, scaffold.Manifest{
		Name: "greet", Version: "0.1.0", Description: "g", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"init", "-q"}, {"remote", "add", "origin", bare}} {
		if out, err := exec.Command("git", append([]string{"-C", tool}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	t.Chdir(tool)

	if err := cmdPublish(p, []string{"--private"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The bare remote now has the tag and at least one commit.
	tags, _ := exec.Command("git", "-C", bare, "tag").Output()
	if !strings.Contains(string(tags), "v0.1.0") {
		t.Fatalf("expected v0.1.0 pushed to origin, got tags: %q", tags)
	}
	n, _ := exec.Command("git", "-C", bare, "rev-list", "--all", "--count").Output()
	if strings.TrimSpace(string(n)) == "0" {
		t.Fatal("expected commits pushed to origin")
	}
}

func TestPublishRequiresVisibility(t *testing.T) {
	p := testPaths(t)
	dir := filepath.Join(t.TempDir(), "greet")
	scaffold.Create(dir, scaffold.Manifest{Name: "greet", Version: "0.1.0", Description: "g", Runtime: "bash"})
	t.Chdir(dir)
	if err := cmdPublish(p, nil); err == nil {
		t.Fatal("publish must require --public or --private")
	}
	if err := cmdPublish(p, []string{"--public", "--private"}); err == nil {
		t.Fatal("publish must reject both --public and --private")
	}
}

// TestSensitiveStagedPaths guards the `git add -A` secret-leak: publish must
// refuse to commit files that look like secrets (the keygen default
// smith-allowlist.key, private keys, .env), so a stray secret in a tool dir
// isn't pushed to a public repo.
func TestSensitiveStagedPaths(t *testing.T) {
	staged := []string{
		"manifest.yaml", "tool.py", "SKILL.md", // fine
		"smith-allowlist.key", // the documented vector (keygen default)
		"secrets/deploy.pem",  // private key, nested
		".env",                // dotenv
		".env.production",     // dotenv variant
		"id_ed25519",          // ssh private key
	}
	got := sensitiveStagedPaths(staged)
	want := map[string]bool{
		"smith-allowlist.key": true, "secrets/deploy.pem": true,
		".env": true, ".env.production": true, "id_ed25519": true,
	}
	if len(got) != len(want) {
		t.Fatalf("flagged %v, want the %d secret-looking paths", got, len(want))
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("flagged non-secret %q", g)
		}
	}
}

func TestVisibilityMismatch(t *testing.T) {
	cases := []struct {
		wantPublic bool
		actual     string
		mismatch   bool
	}{
		{true, "PUBLIC", false},
		{true, "private", true},
		{false, "PRIVATE", false},
		{false, "public", true},
		{false, "internal", true}, // internal is not private
		{true, "internal", true},  // nor public
		{true, "", false},         // unknown: can't assert a mismatch
	}
	for _, c := range cases {
		if got := visibilityMismatch(c.wantPublic, c.actual) != ""; got != c.mismatch {
			t.Errorf("visibilityMismatch(public=%v, %q) mismatch=%v, want %v", c.wantPublic, c.actual, got, c.mismatch)
		}
	}
}

// TestPublishRefusesStagedSecret drives the real publish flow: a stray private
// key in the tool dir must abort the publish before any commit/push.
func TestPublishRefusesStagedSecret(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	p := testPaths(t)
	tool := filepath.Join(t.TempDir(), "greet")
	if err := scaffold.Create(tool, scaffold.Manifest{
		Name: "greet", Version: "0.1.0", Description: "g", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	// A local bare origin so a guard regression can never reach real GitHub.
	bare := filepath.Join(t.TempDir(), "origin.git")
	exec.Command("git", "init", "--bare", "-q", bare).Run()
	exec.Command("git", "-C", tool, "init", "-q").Run()
	exec.Command("git", "-C", tool, "remote", "add", "origin", bare).Run()
	// A stray keygen key, force-staged past the scaffolded .gitignore — the
	// backstop must catch a secret the ignore file didn't (e.g. a repo that
	// predates it, or a force-add).
	os.WriteFile(filepath.Join(tool, "smith-allowlist.key"), []byte("SECRET"), 0o600)
	exec.Command("git", "-C", tool, "add", "-f", "smith-allowlist.key").Run()
	t.Chdir(tool)
	err := cmdPublish(p, []string{"--private"})
	if err == nil || !strings.Contains(err.Error(), "look like secrets") {
		t.Fatalf("publish must refuse a staged secret, got: %v", err)
	}
}
