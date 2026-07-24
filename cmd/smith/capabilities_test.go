package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/sandbox"
	"smith/internal/scaffold"
)

func mustPerms(t *testing.T, m scaffold.Manifest) sandbox.Perms {
	t.Helper()
	p, err := manifestPerms(m)
	if err != nil {
		t.Fatalf("manifestPerms(%+v): unexpected error: %v", m, err)
	}
	return p
}

func TestManifestPermsMapping(t *testing.T) {
	// nil sandbox → strict default.
	if p := mustPerms(t, scaffold.Manifest{}); p.Network || len(p.WritePaths) != 0 {
		t.Fatalf("nil sandbox must be strict, got %+v", p)
	}

	home, _ := os.UserHomeDir()
	m := scaffold.Manifest{Sandbox: &scaffold.SandboxSpec{
		Network: true,
		Write:   []string{"~/.cache/tool", "/var/lib/tool", ""},
	}}
	p := mustPerms(t, m)
	if !p.Network {
		t.Fatal("network grant not mapped")
	}
	if len(p.WritePaths) != 2 { // empty string dropped
		t.Fatalf("expected 2 write paths, got %v", p.WritePaths)
	}
	wantHome := filepath.Join(home, ".cache/tool")
	found := false
	for _, w := range p.WritePaths {
		if w == wantHome {
			found = true
		}
	}
	if !found {
		t.Fatalf("~ not expanded to $HOME: %v (want %s)", p.WritePaths, wantHome)
	}
}

func TestManifestPermsRejectsDangerousGrants(t *testing.T) {
	home, _ := os.UserHomeDir()
	bad := []string{
		"/", "/etc", "/etc/cron.d", "~", "~/", "~/.ssh", "~/.ssh/authorized_keys", "~/.aws", home,
		// Relative grants resolve into the project, not the self-test scratch
		// dir: the .git/hooks persistence escape and its cousins.
		".git/hooks", ".", "..", "../evil", "subdir",
		// Hidden files/dirs directly under $HOME: shell rc + config +
		// skill-injection persistence surface.
		"~/.zshrc", "~/.bashrc", "~/.profile", "~/.config", "~/.claude", "~/.gitconfig",
		// macOS launch agents.
		"~/Library/LaunchAgents", "~/Library/LaunchAgents/x.plist",
	}
	for _, w := range bad {
		if w == "" {
			continue
		}
		_, err := manifestPerms(scaffold.Manifest{Sandbox: &scaffold.SandboxSpec{Write: []string{w}}})
		if err == nil {
			t.Fatalf("write grant %q must be refused", w)
		}
	}
	// A benign grant beyond CWD/TMP (a named subpath, not a hidden entry
	// directly under $HOME) is still allowed.
	if _, err := manifestPerms(scaffold.Manifest{Sandbox: &scaffold.SandboxSpec{Write: []string{"~/.cache/tool"}}}); err != nil {
		t.Fatalf("benign grant rejected: %v", err)
	}
}

// TestForbiddenWriteGrantHomeIsAllowlistOnly pins the class invariant behind the
// whole family of home-persistence bypasses (~/.claude/skills, ~/Library[/…],
// ~/bin, ~/.config/*, cross-user homes): a write grant anywhere under $HOME is
// refused UNLESS it lands in an explicitly-blessed regenerable scratch root
// (~/.cache). Denylisting individual bad subtrees kept leaving siblings (a
// parent dir, a non-hidden PATH dir, a case-folded name, another user's home),
// so the model is allowlist-only. Grants into any user home tree are also
// refused unconditionally, which fails closed when $HOME can't be determined.
func TestForbiddenWriteGrantHomeIsAllowlistOnly(t *testing.T) {
	home := "/home/tester"
	denied := []string{
		home + "/.claude/skills",         // skill injection (the original bypass)
		home + "/.claude/skills/evil.md", // deeper still
		home + "/.config/autostart",      // XDG autostart → runs on login
		home + "/.local/bin",             // PATH shadowing
		home + "/.vim/plugin",            // sourced on editor start
		home + "/.ssh/authorized_keys",   // credential store
		home + "/.zshrc",                 // shell rc
		home + "/Library",                // Finding 1: parent of ~/Library/LaunchAgents
		home + "/Library/LaunchAgents/x.plist",
		home + "/bin",                 // Finding 2: non-hidden PATH-shadow dir
		home + "/Documents",           // ordinary non-hidden dir — still not scratch
		home + "/work/build",          // arbitrary nested dir
		"/home/victim/.claude/skills", // another user's home (cross-account)
		"/root/.bashrc",               // root's home
		"/Users/victim/Library/LaunchAgents/x.plist",
	}
	for _, abs := range denied {
		if reason := forbiddenWriteGrant(abs, home); reason == "" {
			t.Errorf("forbiddenWriteGrant(%q, %q) allowed it; want refusal", abs, home)
		}
	}
	// Fail closed when $HOME is unset (CI/systemd/cron): a literal home-tree path
	// must still be refused even though the home-relative checks can't run.
	for _, abs := range []string{"/home/runner/.bashrc", "/root/.ssh/authorized_keys", "/Users/x/Library"} {
		if reason := forbiddenWriteGrant(abs, ""); reason == "" {
			t.Errorf("forbiddenWriteGrant(%q, \"\") allowed a home tree with $HOME unset; want refusal", abs)
		}
	}
	// Only the blessed scratch root (and its subtree) is grantable under $HOME.
	// A write path entirely outside any home tree stays allowed.
	allowed := []string{
		home + "/.cache",
		home + "/.cache/tool",
		home + "/.cache/tool/sub",
		"/var/lib/tool", // outside all home trees
	}
	for _, abs := range allowed {
		if reason := forbiddenWriteGrant(abs, home); reason != "" {
			t.Errorf("forbiddenWriteGrant(%q, %q) refused it (%s); want allowed", abs, home, reason)
		}
	}
}

func TestCapabilitySummary(t *testing.T) {
	if capabilitySummary(mustPerms(t, scaffold.Manifest{})) != "" {
		t.Fatal("no capabilities → empty summary")
	}
	s := capabilitySummary(mustPerms(t, scaffold.Manifest{
		Sandbox: &scaffold.SandboxSpec{Network: true, Write: []string{"/x"}},
	}))
	if !strings.Contains(s, "network egress") || !strings.Contains(s, "write: /x") {
		t.Fatalf("summary should name network + write: %q", s)
	}
}
