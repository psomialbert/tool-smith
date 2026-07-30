package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealStaleActivation(t *testing.T) {
	newLine := `_.path = ["/proj/.smith/bin"]`

	// A stale v1 line (points into the retired ~/.toolsmith store) is rewritten.
	stale := []string{
		"# managed by smith: puts the Toolsmith trusted store on PATH",
		"[env]",
		`_.path = ["/Users/x/.toolsmith/store/trusted/bin"]`,
	}
	got, ok := healStaleActivation(append([]string{}, stale...), newLine)
	if !ok {
		t.Fatal("expected stale v1 activation to be healed")
	}
	if got[2] != newLine {
		t.Fatalf("stale line not rewritten: %q", got[2])
	}

	// A smith-managed file carrying ANOTHER machine's absolute .smith/bin path
	// (e.g. a committed mise.local.toml) is also healed — it's still ours.
	foreign := []string{
		"# managed by smith: puts adopted tools on PATH",
		"[env]",
		`_.path = ["/Users/someone-else/proj/.smith/bin"]`,
	}
	got, ok = healStaleActivation(append([]string{}, foreign...), newLine)
	if !ok || got[2] != newLine {
		t.Fatalf("expected a committed foreign smith path to be healed, got ok=%v line=%q", ok, got[2])
	}

	// A user's own _.path (no smith header) is left untouched.
	userOwned := []string{"[env]", `_.path = ["/opt/tools/bin"]`}
	if _, ok := healStaleActivation(append([]string{}, userOwned...), newLine); ok {
		t.Fatal("must not rewrite a user-authored _.path")
	}
}

func TestEnsureProjectGitignoreIgnoresMiseLocal(t *testing.T) {
	dir := t.TempDir()
	restore, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(restore)

	ensureProjectGitignore()
	raw, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatalf("no .gitignore written: %v", err)
	}
	for _, want := range []string{"mise.local.toml", smithDir + "/bin/", smithDir + "/cache/"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, raw)
		}
	}
}

func TestEnsureActivationHealsStaleFile(t *testing.T) {
	proj := t.TempDir()
	mise := filepath.Join(proj, "mise.local.toml")
	// A real v1 mise.local.toml pointing at the retired trusted store.
	if err := os.WriteFile(mise, []byte(
		"# managed by smith: puts the Toolsmith trusted store on PATH\n"+
			"[env]\n_.path = [\"/Users/x/.toolsmith/store/trusted/bin\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(proj, ".smith", "bin")

	// The v1 behavior errored here ("add it manually"); v2 must heal instead.
	if err := ensureActivation(proj, binDir); err != nil {
		t.Fatalf("ensureActivation should heal a stale v1 file, got: %v", err)
	}
	raw, _ := os.ReadFile(mise)
	s := string(raw)
	if strings.Contains(s, ".toolsmith") {
		t.Fatalf("stale store path not removed:\n%s", s)
	}
	if !strings.Contains(s, binDir) {
		t.Fatalf("healed file does not point at .smith/bin:\n%s", s)
	}
	// Second call is a clean no-op (path already correct).
	if err := ensureActivation(proj, binDir); err != nil {
		t.Fatalf("re-run should be a no-op, got: %v", err)
	}
}

func TestEnsureActivationHealsCommittedForeignPath(t *testing.T) {
	// The peer scenario: someone committed mise.local.toml with their own
	// absolute .smith/bin. A teammate's `smith install` must heal it, not error.
	proj := t.TempDir()
	mise := filepath.Join(proj, "mise.local.toml")
	if err := os.WriteFile(mise, []byte(
		"# managed by smith: puts adopted tools on PATH\n"+
			"[env]\n_.path = [\"/Users/author/repo/.smith/bin\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(proj, ".smith", "bin")
	if err := ensureActivation(proj, binDir); err != nil {
		t.Fatalf("should heal a committed foreign path, got: %v", err)
	}
	raw, _ := os.ReadFile(mise)
	if !strings.Contains(string(raw), binDir) || strings.Contains(string(raw), "/Users/author/") {
		t.Fatalf("foreign path not healed to local binDir:\n%s", raw)
	}
}

func TestEnsureActivationRespectsUserPath(t *testing.T) {
	proj := t.TempDir()
	mise := filepath.Join(proj, "mise.local.toml")
	// A user's own _.path (not a smith v1 store) must not be clobbered.
	if err := os.WriteFile(mise, []byte("[env]\n_.path = [\"/opt/tools/bin\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureActivation(proj, filepath.Join(proj, ".smith", "bin")); err == nil {
		t.Fatal("expected ensureActivation to refuse clobbering a user-authored _.path")
	}
}

func TestStaleConfigKeysStripsRetired(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte(
		"transport:\n  kind: github-release\n  repo: x/y\nreview:\n  default: tui\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dropped, err := staleConfigKeys(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != "transport" {
		t.Fatalf("expected to drop [transport], got %v", dropped)
	}
	raw, _ := os.ReadFile(cfg)
	s := string(raw)
	if strings.Contains(s, "transport") {
		t.Fatalf("transport not stripped:\n%s", s)
	}
	if !strings.Contains(s, "review") {
		t.Fatalf("review (a live key) was lost:\n%s", s)
	}
	// Idempotent: a second run drops nothing.
	if dropped2, _ := staleConfigKeys(cfg); len(dropped2) != 0 {
		t.Fatalf("second run should be a no-op, dropped %v", dropped2)
	}
}

func TestStaleConfigKeysMissingFile(t *testing.T) {
	dropped, err := staleConfigKeys(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil || dropped != nil {
		t.Fatalf("missing config should be a clean no-op, got %v / %v", dropped, err)
	}
}

func TestCmdMigrateBacksUpV1Artifacts(t *testing.T) {
	p := testPaths(t)
	// Seed a v1-shaped home: retired dirs + a config with a dead key.
	for _, d := range []string{"index", "keys", "store", "drafts"} {
		if err := os.MkdirAll(filepath.Join(p.Home, d, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(p.Home, "config.yaml"),
		[]byte("transport:\n  repo: x/y\nreview:\n  default: tui\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cmdMigrate(p, []string{"--quiet"}); err != nil {
		t.Fatal(err)
	}

	// Retired dirs moved out of home...
	for _, d := range []string{"index", "keys", "store", "drafts"} {
		if _, err := os.Stat(filepath.Join(p.Home, d)); !os.IsNotExist(err) {
			t.Fatalf("%s should have been moved out of home", d)
		}
	}
	// ...into a single backup dir, preserving contents.
	entries, _ := os.ReadDir(p.Home)
	var backup string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".v1-backup-") {
			backup = filepath.Join(p.Home, e.Name())
		}
	}
	if backup == "" {
		t.Fatal("no .v1-backup- dir created")
	}
	if _, err := os.Stat(filepath.Join(backup, "keys", "sub")); err != nil {
		t.Fatalf("backup did not preserve contents: %v", err)
	}
	// config stripped, sandbox home still present.
	raw, _ := os.ReadFile(filepath.Join(p.Home, "config.yaml"))
	if strings.Contains(string(raw), "transport") {
		t.Fatalf("config not migrated:\n%s", raw)
	}

	// Idempotent: a clean second run reports nothing to do (no error, no new backup churn).
	if err := cmdMigrate(p, []string{"--quiet"}); err != nil {
		t.Fatalf("second migrate run errored: %v", err)
	}
}

// agents: is a live config key. If it's missing from v2ConfigKeys, `smith
// migrate` strips it as "retired" — silently discarding a user's target
// selection. This test is the guard for that trap.
func TestMigratePreservesAgentsKey(t *testing.T) {
	p := testPaths(t)
	cfg := filepath.Join(p.Home, "config.yaml")
	body := "agents:\n    targets:\n        - agents-md\neditor: vim\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cmdMigrate(p, []string{"--quiet"}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "agents:") {
		t.Errorf("migrate stripped the live agents key:\n%s", raw)
	}
	if !strings.Contains(string(raw), "agents-md") {
		t.Errorf("migrate dropped the targets value:\n%s", raw)
	}
}
