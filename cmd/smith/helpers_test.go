package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"smith/internal/config"
	"smith/internal/sandbox"
)

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	t.Setenv("SMITH_HOME", t.TempDir())
	// Isolate skill export so tests never touch the real ~/.claude/skills.
	t.Setenv("SMITH_SKILLS_DIR", filepath.Join(t.TempDir(), "skills"))
	p, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return p
}

// requireSandbox skips a test unless a *functional* sandbox is available
// (presence on PATH isn't enough — some CI hosts ship bwrap but forbid the
// namespace setup it needs). Probes by running a no-op sandboxed.
func requireSandbox(t *testing.T) {
	t.Helper()
	bin := map[string]string{"darwin": "sandbox-exec", "linux": "bwrap"}[runtime.GOOS]
	if bin == "" {
		t.Skipf("no sandbox on %s", runtime.GOOS)
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%s not available", bin)
	}
	work := t.TempDir()
	tru, err := exec.LookPath("true")
	if err != nil {
		tru = "/usr/bin/true"
	}
	argv, err := sandbox.Command(filepath.Join(work, "profiles"), work, sandbox.Perms{}, []string{tru})
	if err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("sandbox present but not functional here (%v): %s", err, out)
	}
}
