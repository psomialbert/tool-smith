package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findCheck(name string) check {
	for _, c := range doctorChecks() {
		if c.name == name {
			return c
		}
	}
	return check{}
}

func TestRunDoctorReportsAndExits(t *testing.T) {
	p := testPaths(t)
	errCheck := check{
		name: "always-bad", scope: scopeMachine,
		diagnose: func(dc docCtx) []finding {
			return []finding{{Check: "always-bad", Scope: scopeMachine, Severity: sevERROR, Message: "boom"}}
		},
	}
	fs := runDoctor(docCtx{p: p}, []check{errCheck}, false)
	if len(fs) != 1 || fs[0].Severity != sevERROR {
		t.Fatalf("expected one error finding, got %+v", fs)
	}
	if doctorExit(fs) != 1 {
		t.Fatal("an ERROR finding must exit non-zero")
	}
}

func TestRunDoctorAppliesFix(t *testing.T) {
	p := testPaths(t)
	fixed := false
	c := check{
		name: "fixme", scope: scopeMachine,
		diagnose: func(dc docCtx) []finding {
			if fixed {
				return []finding{{Check: "fixme", Scope: scopeMachine, Severity: sevOK, Message: "ok"}}
			}
			return []finding{{Check: "fixme", Scope: scopeMachine, Severity: sevWARN, Message: "needs fix", Fixable: true}}
		},
		apply: func(dc docCtx) error { fixed = true; return nil },
	}
	fs := runDoctor(docCtx{p: p}, []check{c}, true)
	if !fs[0].Fixed || fs[0].Severity != sevOK {
		t.Fatalf("expected finding fixed and OK, got %+v", fs[0])
	}
	if doctorExit(fs) != 0 {
		t.Fatal("after fix there should be no error")
	}
}

func TestRunDoctorSkipsProjectChecksWhenNotInProject(t *testing.T) {
	p := testPaths(t)
	c := check{
		name: "proj", scope: scopeProject,
		diagnose: func(dc docCtx) []finding {
			return []finding{{Check: "proj", Scope: scopeProject, Severity: sevERROR, Message: "x"}}
		},
	}
	fs := runDoctor(docCtx{p: p, projectDir: ""}, []check{c}, false)
	if len(fs) != 0 {
		t.Fatalf("project checks must be skipped with no project, got %+v", fs)
	}
}

func TestRenderJSONShape(t *testing.T) {
	out := renderDoctor(docCtx{projectDir: ""}, []finding{
		{Check: "mise-present", Scope: scopeMachine, Severity: sevOK, Message: "mise present"},
	}, true)
	for _, want := range []string{`"check"`, `"mise-present"`, `"summary"`, `"error"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %q:\n%s", want, out)
		}
	}
}

func TestV1CruftCheckDiagnosesAndFixes(t *testing.T) {
	p := testPaths(t)
	os.MkdirAll(filepath.Join(p.Home, "index"), 0o755)
	os.WriteFile(filepath.Join(p.Home, "config.yaml"),
		[]byte("transport:\n  repo: x/y\nreview:\n  default: tui\n"), 0o644)

	c := findCheck("v1-home-cruft")
	if c.name == "" {
		t.Fatal("v1-home-cruft check not registered")
	}
	dc := docCtx{p: p}
	fs := c.diagnose(dc)
	if fs[0].Severity != sevWARN || !fs[0].Fixable {
		t.Fatalf("expected fixable WARN, got %+v", fs[0])
	}
	if err := c.apply(dc); err != nil {
		t.Fatal(err)
	}
	if fs2 := c.diagnose(dc); fs2[0].Severity != sevOK {
		t.Fatalf("after fix expected OK, got %+v", fs2[0])
	}
}

func TestActivationState(t *testing.T) {
	proj := t.TempDir()
	bin := filepath.Join(proj, ".smith", "bin")
	mise := filepath.Join(proj, "mise.local.toml")

	if got := activationState(proj, bin); got != "missing" {
		t.Fatalf("no file → missing, got %q", got)
	}
	os.WriteFile(mise, []byte("# managed by smith\n[env]\n_.path = [\""+bin+"\"]\n"), 0o644)
	if got := activationState(proj, bin); got != "ok" {
		t.Fatalf("correct path → ok, got %q", got)
	}
	os.WriteFile(mise, []byte("# managed by smith\n[env]\n_.path = [\"/x/.toolsmith/store/bin\"]\n"), 0o644)
	if got := activationState(proj, bin); got != "stale" {
		t.Fatalf("smith-managed foreign → stale, got %q", got)
	}
	os.WriteFile(mise, []byte("[env]\n_.path = [\"/opt/tools/bin\"]\n"), 0o644)
	if got := activationState(proj, bin); got != "foreign" {
		t.Fatalf("user-authored path → foreign, got %q", got)
	}
}

func TestGitignoreCheck(t *testing.T) {
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)
	os.MkdirAll(smithDir, 0o755)

	c := findCheck("gitignore")
	dc := docCtx{projectDir: "."}
	if fs := c.diagnose(dc); fs[0].Severity != sevWARN || !fs[0].Fixable {
		t.Fatalf("empty .gitignore → fixable WARN, got %+v", fs[0])
	}
	if err := c.apply(dc); err != nil {
		t.Fatal(err)
	}
	if fs := c.diagnose(dc); fs[0].Severity != sevOK {
		t.Fatalf("after fix → OK, got %+v", fs[0])
	}
	raw, _ := os.ReadFile(".gitignore")
	if !strings.Contains(string(raw), "mise.local.toml") {
		t.Fatalf("gitignore missing mise.local.toml:\n%s", raw)
	}
}

func seedBashTool(t *testing.T, name string) string {
	t.Helper()
	tdir := filepath.Join(smithDir, "tools", name)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(tdir, name), []byte("#!/bin/bash\necho hi\n"), 0o755)
	os.WriteFile(filepath.Join(tdir, "manifest.yaml"), []byte("name: "+name+"\n"), 0o644)
	os.WriteFile(filepath.Join(tdir, "selftest.sh"), []byte("echo ok\n"), 0o644)
	os.WriteFile(filepath.Join(tdir, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: A sharp trigger for "+name+"; use it when you need "+name+".\n---\n# "+name+"\n"), 0o644)
	sum, _ := bundleHash(tdir, sourceFiles("bash", name, name))
	return sum
}

func TestVendoredShaCheckDetectsDrift(t *testing.T) {
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)

	sum := seedBashTool(t, "greet")
	lock := ProjLock{Tools: map[string]ProjTool{
		"greet": {Source: "x/greet", Version: "v1", Sha256: sum, Entrypoint: "greet", Runtime: "bash"},
	}}
	dc := docCtx{projectDir: ".", lock: lock}
	c := findCheck("vendored-sha")

	if fs := c.diagnose(dc); fs[0].Severity != sevOK {
		t.Fatalf("matching sha → OK, got %+v", fs[0])
	}
	os.WriteFile(filepath.Join(smithDir, "tools", "greet", "greet"), []byte("#!/bin/bash\necho tampered\n"), 0o755)
	if fs := c.diagnose(dc); fs[0].Severity != sevERROR {
		t.Fatalf("drift → ERROR, got %+v", fs[0])
	}
	// A completely MISSING vendored dir must be reported as missing, not as a
	// sha mismatch (bundleHash skips absent files → empty hash → false "≠ pin").
	os.RemoveAll(filepath.Join(smithDir, "tools", "greet"))
	fs := c.diagnose(dc)
	if fs[0].Severity != sevERROR || !strings.Contains(fs[0].Message, "no vendored source") {
		t.Fatalf("missing dir → 'no vendored source' error, got %+v", fs[0])
	}
}

func TestInstalledCheckDetectsMissing(t *testing.T) {
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)

	sum := seedBashTool(t, "greet")
	lock := ProjLock{Tools: map[string]ProjTool{
		"greet": {Source: "x/greet", Version: "v1", Sha256: sum, Entrypoint: "greet", Runtime: "bash"},
	}}
	dc := docCtx{projectDir: ".", lock: lock}
	c := findCheck("installed")

	if fs := c.diagnose(dc); fs[0].Severity != sevWARN || !fs[0].Fixable {
		t.Fatalf("not-on-bin → fixable WARN, got %+v", fs[0])
	}
	if err := c.apply(dc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(smithDir, "bin", "greet")); err != nil {
		t.Fatalf("apply should materialize greet on .smith/bin: %v", err)
	}
	if fs := c.diagnose(dc); fs[0].Severity != sevOK {
		t.Fatalf("after fix → OK, got %+v", fs[0])
	}
}

func TestSkillTriggerCheck(t *testing.T) {
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)
	tdir := filepath.Join(smithDir, "tools", "greet")
	os.MkdirAll(tdir, 0o755)
	lock := ProjLock{Tools: map[string]ProjTool{"greet": {Entrypoint: "greet", Runtime: "bash"}}}
	dc := docCtx{projectDir: ".", lock: lock}
	c := findCheck("skill-trigger")
	if c.name == "" {
		t.Fatal("skill-trigger check not registered")
	}

	os.WriteFile(filepath.Join(tdir, "SKILL.md"),
		[]byte("---\nname: greet\ndescription: print a greeting. TODO: sharpen the trigger — name the situations\n---\n# greet\n"), 0o644)
	if fs := c.diagnose(dc); fs[0].Severity != sevWARN {
		t.Fatalf("placeholder → WARN, got %+v", fs[0])
	}

	os.WriteFile(filepath.Join(tdir, "SKILL.md"),
		[]byte("---\nname: greet\ndescription: Print a greeting; use when a session needs a canned hello line.\n---\n# greet\n"), 0o644)
	if fs := c.diagnose(dc); fs[0].Severity != sevOK {
		t.Fatalf("sharpened → OK, got %+v", fs[0])
	}

	// A valid prose trigger containing ": " (strict-YAML-invalid but harness-OK)
	// must NOT be a false positive — the real det-canary case.
	os.WriteFile(filepath.Join(tdir, "SKILL.md"),
		[]byte("---\nname: greet\ndescription: Greet someone. Also `--parse` mode: score a captured blob without re-running.\n---\n# greet\n"), 0o644)
	if fs := c.diagnose(dc); fs[0].Severity != sevOK {
		t.Fatalf("prose trigger with \": \" must be OK, got %+v", fs[0])
	}
}

func TestDoctorEndToEndProjectFix(t *testing.T) {
	requireSandbox(t) // sandbox-functional is a machine check
	p := testPaths(t)
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)

	sum := seedBashTool(t, "greet")
	saveProjLock(ProjLock{Tools: map[string]ProjTool{
		"greet": {Source: "x/greet", Version: "v1", Sha256: sum, Entrypoint: "greet", Runtime: "bash"},
	}})

	dc := resolveDocCtx(p)
	if dc.projectDir == "" {
		t.Fatal("expected project context")
	}
	before := runDoctor(dc, doctorChecks(), false)
	if doctorExit(before) == 0 {
		t.Fatalf("expected issues before fix, got %+v", before)
	}
	after := runDoctor(dc, doctorChecks(), true)
	for _, f := range after {
		if f.Check == "activation" && f.Severity != sevOK {
			t.Fatalf("activation not fixed: %+v", f)
		}
		if f.Check == "installed" && f.Severity != sevOK {
			t.Fatalf("installed not fixed: %+v", f)
		}
	}
	if _, err := os.Lstat(filepath.Join(smithDir, "bin", "greet")); err != nil {
		t.Fatalf("greet should be materialized: %v", err)
	}
}

// TestDoctorRejectsBothScopes: --machine and --project are mutually exclusive;
// passing both must error, not silently pick one.
func TestDoctorRejectsBothScopes(t *testing.T) {
	err := cmdDoctor(testPaths(t), []string{"--machine", "--project"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("--machine --project must error, got: %v", err)
	}
}

func findFinding(fs []finding, check string) (finding, bool) {
	for _, f := range fs {
		if f.Check == check {
			return f, true
		}
	}
	return finding{}, false
}

// A project with tools adopted and a current block is healthy.
func TestAgentsBlockCheckOK(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v1", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	block := renderAgentBlock(projectToolIndex(lock))
	if err := os.WriteFile("AGENTS.md", []byte("# P\n\n"+block), 0o644); err != nil {
		t.Fatal(err)
	}

	dc := docCtx{p: p, projectDir: ".", lock: lock}
	f, ok := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if !ok {
		t.Fatal("agents-block check did not report")
	}
	if f.Severity != sevOK {
		t.Errorf("severity = %q (%s); want ok", f.Severity, f.Message)
	}
}

// A stale block is a fixable warning, and --fix regenerates it.
func TestAgentsBlockCheckStaleIsFixed(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v2", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	stale := agentBlockBeginPrefix + " -->\n## Project tools (smith)\n\nSTALE\n" + agentBlockEnd + "\n"
	if err := os.WriteFile("AGENTS.md", []byte("# P\n\n"+stale), 0o644); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: lock}

	f, _ := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if f.Severity != sevWARN || !f.Fixable {
		t.Fatalf("stale block should be a fixable warn, got %q fixable=%v", f.Severity, f.Fixable)
	}

	f, _ = findFinding(runDoctor(dc, doctorChecks(), true), "agents-block")
	if f.Severity != sevOK || !f.Fixed {
		t.Errorf("--fix should refresh the block; got %q fixed=%v (%s)", f.Severity, f.Fixed, f.Message)
	}
	got, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "STALE") {
		t.Errorf("stale content survived --fix:\n%s", got)
	}
}

// No agent instruction file at all: informational, not a warning — plenty of
// projects deliberately have none. --fix creates AGENTS.md (never CLAUDE.md).
func TestAgentsBlockCheckMissingFileIsInfoAndFixCreatesAgentsMD(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v1", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: lock}

	f, _ := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if f.Severity != sevINFO {
		t.Errorf("missing agent file should be info, got %q", f.Severity)
	}
	if !f.Fixable {
		t.Error("missing agent file should be fixable")
	}

	runDoctor(dc, doctorChecks(), true)
	if _, err := os.Stat("AGENTS.md"); err != nil {
		t.Errorf("--fix should have created AGENTS.md: %v", err)
	}
	if _, err := os.Stat("CLAUDE.md"); !os.IsNotExist(err) {
		t.Error("--fix must not create CLAUDE.md; that would presume Claude")
	}
}

// Nothing adopted: an empty table is noise, so stay silent.
func TestAgentsBlockCheckSilentWithNoTools(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveProjLock(ProjLock{Tools: map[string]ProjTool{}}); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: ProjLock{Tools: map[string]ProjTool{}}}

	if f, ok := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block"); ok {
		t.Errorf("no tools adopted — check should not report, got %q", f.Message)
	}
}

// TestAgentsBlockCheckStaleBlockAfterLastToolRemoved is F7's regression guard:
// `cmdInstall` errors out before reaching emitAgentTargets when the lockfile
// has zero tools, and (pre-fix) this check returned nil outright whenever
// len(dc.lock.Tools) == 0 — so un-adopting a project's last tool left a block
// still listing it, with doctor staying silent forever. This must now be
// reported as fixable, and --fix must replace the stale listing with
// renderAgentBlock(nil)'s "No tools adopted yet" message.
func TestAgentsBlockCheckStaleBlockAfterLastToolRemoved(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The lockfile now has no tools — as if the last one was just un-adopted —
	// but AGENTS.md still carries the old block listing "calc".
	emptyLock := ProjLock{Tools: map[string]ProjTool{}}
	if err := saveProjLock(emptyLock); err != nil {
		t.Fatal(err)
	}
	staleBlock := renderAgentBlock([]toolEntry{{Name: "calc", Version: "v1", Runtime: "bash", Invoke: "calc --json …", Trigger: "x"}})
	if err := os.WriteFile("AGENTS.md", []byte("# P\n\n"+staleBlock), 0o644); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: emptyLock}

	f, ok := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if !ok {
		t.Fatal("un-adopting the last tool must still be diagnosed — a stale block was left behind")
	}
	if f.Severity != sevWARN || !f.Fixable {
		t.Fatalf("stale block after the last tool was removed should be a fixable WARN, got %q fixable=%v (%s)", f.Severity, f.Fixable, f.Message)
	}

	runDoctor(dc, doctorChecks(), true)
	got, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "calc") {
		t.Errorf("--fix left the stale tool listing behind:\n%s", got)
	}
	if !strings.Contains(string(got), "No tools adopted yet") {
		t.Errorf("--fix should replace the stale block with the empty-state message:\n%s", got)
	}
}

// sevINFO must not fail the exit code.
func TestInfoSeverityDoesNotFailExit(t *testing.T) {
	if got := doctorExit([]finding{{Severity: sevINFO}}); got != 0 {
		t.Errorf("doctorExit with info = %d; want 0", got)
	}
}

// With both files present, a stale CLAUDE.md beside a current AGENTS.md must
// still be reported and fixed — emitAgentTargets writes both, so checking only
// the first would leave one silently rotting.
func TestAgentsBlockCheckDetectsStaleSecondFile(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v1", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	block := renderAgentBlock(projectToolIndex(lock))
	// AGENTS.md current, CLAUDE.md stale.
	if err := os.WriteFile("AGENTS.md", []byte("# P\n\n"+block), 0o644); err != nil {
		t.Fatal(err)
	}
	staleBlock := agentBlockBeginPrefix + " -->\nSTALE\n" + agentBlockEnd + "\n"
	if err := os.WriteFile("CLAUDE.md", []byte("# P\n\n"+staleBlock), 0o644); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: lock}

	f, _ := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if f.Severity != sevWARN {
		t.Fatalf("stale CLAUDE.md not reported: severity %q (%s)", f.Severity, f.Message)
	}
	if !strings.Contains(f.Message, "CLAUDE.md") {
		t.Errorf("message should name the stale file, got: %s", f.Message)
	}

	runDoctor(dc, doctorChecks(), true)
	got, err := os.ReadFile("CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "STALE") {
		t.Errorf("--fix did not refresh CLAUDE.md:\n%s", got)
	}
}

// --- Fix round 1: an unreadable-but-present file must never read as absent,
// and the check must honor agents.targets instead of assuming every *-md
// file is always in scope. ---

// An existing but unreadable AGENTS.md (e.g. mode 0000) must not be treated
// as "no AGENTS.md" — that misreading sent --fix down the create path, where
// writeFileAtomic's rename clobbered the user's real content.
func TestAgentsBlockCheckUnreadableFileIsNotTreatedAsAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions; this test needs a real permission denial")
	}
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v1", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	userContent := "# My real project\n\nHand-written notes; do not touch.\n"
	if err := os.WriteFile("AGENTS.md", []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod("AGENTS.md", 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod("AGENTS.md", 0o644) }) // let TempDir cleanup remove it
	dc := docCtx{p: p, projectDir: ".", lock: lock}

	f, ok := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if !ok {
		t.Fatal("agents-block check did not report")
	}
	if f.Severity == sevINFO || strings.Contains(f.Message, "no AGENTS.md") {
		t.Errorf("an unreadable existing file must not be reported as absent, got %q (%s)", f.Severity, f.Message)
	}
	if f.Fixable {
		t.Errorf("an unreadable file's state can't be safely fixed, got Fixable=true (%s)", f.Message)
	}

	runDoctor(dc, doctorChecks(), true)
	if err := os.Chmod("AGENTS.md", 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != userContent {
		t.Errorf("--fix must not touch an unreadable file's content:\ngot:  %q\nwant: %q", got, userContent)
	}
}

// A dangling symlink at AGENTS.md counts as present (matching mdTarget.detect,
// which uses Lstat) and must never be replaced by --fix with a regular file —
// exactly the case upsertManagedBlock's own symlink guard exists to refuse.
func TestAgentsBlockCheckDanglingSymlinkNotReplaced(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v1", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("does-not-exist-target.md", "AGENTS.md"); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: lock}

	f, ok := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if !ok {
		t.Fatal("agents-block check did not report")
	}
	if f.Severity == sevINFO {
		t.Errorf("a dangling symlink must not be reported as absent, got %q (%s)", f.Severity, f.Message)
	}

	runDoctor(dc, doctorChecks(), true)
	fi, err := os.Lstat("AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("--fix replaced the dangling symlink with a regular file")
	}
}

// agents.targets: ["agents-md"] means doctor must never warn about, or write,
// a stale CLAUDE.md — smith install was never configured to touch it either.
func TestAgentsBlockCheckHonorsConfiguredTargets(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Home, "config.yaml"), []byte("agents:\n  targets: [\"agents-md\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v1", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	block := renderAgentBlock(projectToolIndex(lock))
	if err := os.WriteFile("AGENTS.md", []byte("# P\n\n"+block), 0o644); err != nil {
		t.Fatal(err)
	}
	staleBlock := agentBlockBeginPrefix + " -->\nSTALE\n" + agentBlockEnd + "\n"
	staleContent := "# P\n\n" + staleBlock
	if err := os.WriteFile("CLAUDE.md", []byte(staleContent), 0o644); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: lock}

	f, ok := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if !ok {
		t.Fatal("agents-block check did not report")
	}
	if f.Severity != sevOK {
		t.Errorf("current AGENTS.md with claude-md un-configured must be ok, got %q (%s)", f.Severity, f.Message)
	}
	if strings.Contains(f.Message, "CLAUDE.md") {
		t.Errorf("message must not mention the un-configured CLAUDE.md: %s", f.Message)
	}

	runDoctor(dc, doctorChecks(), true)
	got, err := os.ReadFile("CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != staleContent {
		t.Error("--fix must not write CLAUDE.md when agents.targets excludes claude-md")
	}
}

// agents.targets: [] is an explicit opt-out (distinct from the key being
// absent) — the check must stay silent and --fix must create nothing.
func TestAgentsBlockCheckSilentWhenTargetsEmpty(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Home, "config.yaml"), []byte("agents:\n  targets: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v1", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: lock}

	if f, ok := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block"); ok {
		t.Errorf("agents.targets: [] opts out entirely — check should not report, got %q", f.Message)
	}

	runDoctor(dc, doctorChecks(), true)
	if _, err := os.Stat("AGENTS.md"); !os.IsNotExist(err) {
		t.Error("--fix must not create AGENTS.md when agents.targets is explicitly empty")
	}
	if _, err := os.Stat("CLAUDE.md"); !os.IsNotExist(err) {
		t.Error("--fix must not create CLAUDE.md when agents.targets is explicitly empty")
	}
}

// TestAgentsBlockCheckDirectoryNotTreatedAsPresent is F3's regression guard:
// mdTarget.detect (agents.go) skips a directory at the target path via
// `!fi.IsDir()`, so `smith install` never touches it. Pre-fix, agentBlockState
// counted a directory as present, then os.ReadFile on it failed, turning into
// a permanent, non-fixable sevERROR that `smith doctor` would report forever
// on a state install deliberately ignores.
func TestAgentsBlockCheckDirectoryNotTreatedAsPresent(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v1", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	// AGENTS.md is a directory, not a file.
	if err := os.MkdirAll("AGENTS.md", 0o755); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: lock}

	f, ok := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if !ok {
		t.Fatal("agents-block check did not report")
	}
	if f.Severity == sevERROR {
		t.Errorf("a directory at AGENTS.md must not be a permanent ERROR — install ignores it the same way, got %q (%s)", f.Severity, f.Message)
	}
	// With CLAUDE.md also selected by default and absent, this should read as
	// "no [instruction file]" (info, fixable) — a directory doesn't count as
	// present, matching mdTarget.detect.
	if f.Severity != sevINFO {
		t.Errorf("expected the directory to be treated as absent (info, not present), got %q (%s)", f.Severity, f.Message)
	}
}

// TestAgentsBlockCheckDuplicateMarkersReportedNotOK is F4's regression guard:
// with two "smith:begin" markers in a file (one of them "current"), pre-fix
// agentBlockState's `strings.Contains(text, want)` check passed — since the
// current block IS a substring of the file — reporting sevOK, while every
// `smith install` prints a "2 smith blocks" warning via upsertManagedBlock's
// own refusal. A perpetual "ok" from doctor can never diagnose a perpetual
// warning from install.
func TestAgentsBlockCheckDuplicateMarkersReportedNotOK(t *testing.T) {
	p := testPaths(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Version: "v1", Runtime: "bash", Entrypoint: "calc"},
	}}
	if err := saveProjLock(lock); err != nil {
		t.Fatal(err)
	}
	block := renderAgentBlock(projectToolIndex(lock))
	dup := block + "\n" + block // two begin markers; the first is current
	if err := os.WriteFile("AGENTS.md", []byte("# P\n\n"+dup), 0o644); err != nil {
		t.Fatal(err)
	}
	dc := docCtx{p: p, projectDir: ".", lock: lock}

	f, ok := findFinding(runDoctor(dc, doctorChecks(), false), "agents-block")
	if !ok {
		t.Fatal("agents-block check did not report")
	}
	if f.Severity == sevOK {
		t.Fatalf("duplicate smith blocks must not read as ok — install warns about this forever (%s)", f.Message)
	}
	if f.Fixable {
		t.Errorf("ambiguous duplicate markers cannot be safely auto-fixed (no way to tell which is authoritative), got Fixable=true (%s)", f.Message)
	}
	if !strings.Contains(f.Message, "2 smith blocks") {
		t.Errorf("message should mirror upsertManagedBlock's own wording, got: %s", f.Message)
	}

	// --fix must leave the ambiguous file alone: there is no safe rewrite.
	runDoctor(dc, doctorChecks(), true)
	got, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# P\n\n"+dup {
		t.Errorf("--fix must not touch a file with duplicate smith blocks:\n%s", got)
	}
}
