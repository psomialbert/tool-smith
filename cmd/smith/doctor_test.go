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
