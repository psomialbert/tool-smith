package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"smith/internal/index"
	"smith/internal/scaffold"
)

func requireSandbox(t *testing.T) {
	t.Helper()
	bin := map[string]string{"darwin": "sandbox-exec", "linux": "bwrap"}[runtime.GOOS]
	if bin == "" {
		t.Skip("no sandbox on", runtime.GOOS)
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%s not available", bin)
	}
}

func TestNewRegisterLandsAtTier0(t *testing.T) {
	requireSandbox(t)
	p := testPaths(t)
	// The template placeholder passes the scaffolded contract checks, so a
	// fresh scaffold registers as-is.
	if err := scaffold.Create(draftDir(p, "demo"), scaffold.Manifest{
		Name: "demo", Version: "0.1.0", Description: "test tool", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRegister(p, []string{"demo"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	e, err := index.Load(p.IndexTools, "demo", "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if e.Tier != 0 {
		t.Fatalf("tier = %d, want 0", e.Tier)
	}
	if len(e.Artifacts) != 1 || len(e.Docs) == 0 {
		t.Fatalf("expected 1 artifact + pinned docs, got %d/%d", len(e.Artifacts), len(e.Docs))
	}
	// Re-registration of the same version must be refused.
	if err := cmdRegister(p, []string{"demo"}); err == nil {
		t.Fatal("duplicate registration was not refused")
	}
}

func TestFailingSelfTestBlocksRegistration(t *testing.T) {
	requireSandbox(t)
	p := testPaths(t)
	dir := draftDir(p, "broken")
	if err := scaffold.Create(dir, scaffold.Manifest{
		Name: "broken", Version: "0.1.0", Description: "always fails", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "selftest.sh"),
		[]byte("#!/bin/bash\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmdRegister(p, []string{"broken"}); err == nil {
		t.Fatal("registration succeeded despite failing self-test")
	}
	if _, err := index.Load(p.IndexTools, "broken", "0.1.0"); err == nil {
		t.Fatal("index entry exists despite failed self-test")
	}
	if _, err := os.Stat(filepath.Join(p.StoreQuarantine, "broken")); !os.IsNotExist(err) {
		t.Fatal("quarantine dir exists despite failed self-test")
	}
}

func TestSelfTestRunsSandboxed(t *testing.T) {
	requireSandbox(t)
	if runtime.GOOS != "darwin" {
		t.Skip("escape assertion written for the darwin profile")
	}
	p := testPaths(t)
	dir := draftDir(p, "escapee")
	if err := scaffold.Create(dir, scaffold.Manifest{
		Name: "escapee", Version: "0.1.0", Description: "tries to escape", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	// A self-test that requires writing outside CWD/TMP must fail, proving
	// the self-test itself executes under the sandbox.
	script := "#!/bin/bash\ntouch \"$HOME/smith-escape-proof\" || exit 1\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "selftest.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := cmdRegister(p, []string{"escapee"})
	if err == nil {
		os.Remove(filepath.Join(os.Getenv("HOME"), "smith-escape-proof"))
		t.Fatal("self-test wrote to $HOME — it is not sandboxed")
	}
}
