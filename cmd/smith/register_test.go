package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"smith/internal/index"
	"smith/internal/sandbox"
	"smith/internal/scaffold"
)

// requireSandbox skips a test unless a *functional* sandbox is available.
// Presence on PATH isn't enough: some environments (e.g. GitHub Actions'
// unprivileged Ubuntu runners) ship bwrap but forbid the network-namespace
// setup `--unshare-net` needs, so bwrap aborts. Probe by actually running a
// no-op sandboxed, and skip if it fails — smith itself correctly refuses to
// run there (fail closed), which is not something these tests can exercise.
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
	// the self-test itself executes under the sandbox. $HOME inside the
	// sandbox is a scratch dir, so target the REAL user home by absolute
	// path.
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(realHome, "smith-escape-proof")
	script := "#!/bin/bash\ntouch \"" + marker + "\" || exit 1\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "selftest.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmdRegister(p, []string{"escapee"}); err == nil {
		os.Remove(marker)
		t.Fatal("self-test wrote to the real home dir — it is not sandboxed")
	}
}

func TestSelfTestEnvSanitized(t *testing.T) {
	requireSandbox(t)
	p := testPaths(t)
	t.Setenv("SMITH_TEST_SECRET", "boom")
	dir := draftDir(p, "leakcheck")
	if err := scaffold.Create(dir, scaffold.Manifest{
		Name: "leakcheck", Version: "0.1.0", Description: "asserts env is clean", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	// Passes ONLY if the secret is absent and HOME is not the real home.
	realHome, _ := os.UserHomeDir()
	script := "#!/bin/bash\n" +
		"[ -z \"${SMITH_TEST_SECRET:-}\" ] || { echo leaked >&2; exit 1; }\n" +
		"[ \"$HOME\" != \"" + realHome + "\" ] || { echo real-home >&2; exit 1; }\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "selftest.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmdRegister(p, []string{"leakcheck"}); err != nil {
		t.Fatalf("sandbox env is not sanitized: %v", err)
	}
}
