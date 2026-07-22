package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/scaffold"
)

func TestManifestPermsMapping(t *testing.T) {
	// nil sandbox → strict default.
	if p := manifestPerms(scaffold.Manifest{}); p.Network || len(p.WritePaths) != 0 {
		t.Fatalf("nil sandbox must be strict, got %+v", p)
	}

	home, _ := os.UserHomeDir()
	m := scaffold.Manifest{Sandbox: &scaffold.SandboxSpec{
		Network: true,
		Write:   []string{"~/.cache/tool", "/var/lib/tool", ""},
	}}
	p := manifestPerms(m)
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

func TestCapabilitySummary(t *testing.T) {
	if capabilitySummary(manifestPerms(scaffold.Manifest{})) != "" {
		t.Fatal("no capabilities → empty summary")
	}
	s := capabilitySummary(manifestPerms(scaffold.Manifest{
		Sandbox: &scaffold.SandboxSpec{Network: true, Write: []string{"/x"}},
	}))
	if !strings.Contains(s, "network egress") || !strings.Contains(s, "write: /x") {
		t.Fatalf("summary should name network + write: %q", s)
	}
}
