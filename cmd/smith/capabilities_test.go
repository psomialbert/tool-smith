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
	bad := []string{"/", "/etc", "/etc/cron.d", "~", "~/", "~/.ssh", "~/.ssh/authorized_keys", "~/.aws", home}
	for _, w := range bad {
		if w == "" {
			continue
		}
		_, err := manifestPerms(scaffold.Manifest{Sandbox: &scaffold.SandboxSpec{Write: []string{w}}})
		if err == nil {
			t.Fatalf("write grant %q must be refused", w)
		}
	}
	// A benign grant beyond CWD/TMP is still allowed.
	if _, err := manifestPerms(scaffold.Manifest{Sandbox: &scaffold.SandboxSpec{Write: []string{"~/.cache/tool"}}}); err != nil {
		t.Fatalf("benign grant rejected: %v", err)
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
