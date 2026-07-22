# smith doctor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `smith doctor` command that proactively diagnoses machine- and project-level health and optionally repairs safe issues with `--fix`.

**Architecture:** A check registry (`[]check`) over pure `diagnose`/`apply` halves refactored out of the existing side-effecting helpers (`ensureActivation`, `ensureProjectGitignore`, the sandbox probe, `bundleHash`, `cmdMigrate`). `smith doctor` selects checks by context (machine always; project when `.smith/` exists in cwd), renders a grouped report or `--json`, and exits non-zero only when an ERROR remains.

**Tech Stack:** Go (stdlib + `gopkg.in/yaml.v3`), existing `internal/sandbox` + `internal/config`, `mise` + `git` as external CLIs.

## Global Constraints

- Go, single binary; deps limited to `gopkg.in/yaml.v3` and `golang.org/x/term` (already vendored). No new dependencies.
- Run Go via `mise exec -- go ...` (Go pinned in `mise.toml`).
- `main` is protected: land via a PR with CI green.
- Commit trailer on every commit: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Project constant: `smithDir = ".smith"`; project lockfile `.smith/tools.yaml` via `loadProjLock()`.
- Refactors must not change existing behavior — `install`/`use`/`migrate` tests must still pass.
- Coupling rule (from `CLAUDE.md`): a new command updates the dispatch `switch`, `usage()`, `README.md`, and `CLAUDE.md`.
- `smith doctor` (no flag) is read-only; `--fix` applies only safe/reversible repairs; `--fix` never runs `git`.
- Exit code: `0` if no `error`-severity finding remains, `1` otherwise.

---

### Task 1: Sandbox functional probe

Promote the test-only sandbox probe into a real exported function both `doctor` and the tests can use.

**Files:**
- Modify: `internal/sandbox/sandbox.go` (add `Functional`)
- Test: `internal/sandbox/sandbox_test.go`

**Interfaces:**
- Consumes: `Command(profilesDir, workDir string, perms Perms, argv []string) ([]string, error)`, `Perms{Network bool}`
- Produces: `func Functional(profilesDir string) error` — returns nil iff a no-op command runs successfully under a strict sandbox; a non-nil error explains why not.

- [ ] **Step 1: Write the failing test**

```go
// internal/sandbox/sandbox_test.go  (add)
func TestFunctionalProbe(t *testing.T) {
	// On a host with a working sandbox this must succeed; if the platform has
	// no sandbox binary at all, Functional returns a descriptive error.
	err := Functional(filepath.Join(t.TempDir(), "profiles"))
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if err == nil {
			t.Fatal("expected error on unsupported platform")
		}
		return
	}
	// darwin/linux: either it works, or it fails closed with a message (never panics/empty)
	if err != nil && err.Error() == "" {
		t.Fatal("Functional returned a non-nil but empty error")
	}
}
```

Add imports `path/filepath`, `runtime` to the test file if missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./internal/sandbox/ -run TestFunctionalProbe -v`
Expected: FAIL — `undefined: Functional`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/sandbox/sandbox.go  (add near WriteDefaultProfile)

// Functional reports whether a strict sandbox actually works on this host by
// running a no-op command inside it. Presence of the sandbox binary is not
// enough — some CI hosts ship bwrap but forbid the namespace setup it needs.
// Returns nil on success, or a descriptive error (fails closed).
func Functional(profilesDir string) error {
	bin := map[string]string{"darwin": "sandbox-exec", "linux": "bwrap"}[runtime.GOOS]
	if bin == "" {
		return fmt.Errorf("no sandbox available on %s", runtime.GOOS)
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%s not found on PATH", bin)
	}
	work, err := os.MkdirTemp("", "smith-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	tru, err := exec.LookPath("true")
	if err != nil {
		tru = "/usr/bin/true"
	}
	argv, err := Command(filepath.Join(work, "profiles"), work, Perms{}, []string{tru})
	if err != nil {
		return fmt.Errorf("sandbox setup failed: %w", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sandbox present but not functional: %v: %s", err, out)
	}
	return nil
}
```

Ensure `internal/sandbox/sandbox.go` imports `os`, `os/exec`, `path/filepath`, `runtime`, `fmt` (add any missing).

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./internal/sandbox/ -run TestFunctionalProbe -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/sandbox.go internal/sandbox/sandbox_test.go
git commit -m "sandbox: add Functional() probe for doctor

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Doctor core — types, context, run loop, render, exit, dispatch

Build the harness with the two simplest machine checks (`mise-present`, `sandbox-functional`) so it is end-to-end testable, and wire `smith doctor` into dispatch.

**Files:**
- Create: `cmd/smith/doctor.go`
- Modify: `cmd/smith/main.go` (dispatch `switch` + `usage()`)
- Test: `cmd/smith/doctor_test.go`

**Interfaces:**
- Consumes: `config.Paths{Home, SandboxProfiles}`, `sandbox.Functional`, `smithDir`, `loadProjLock() (ProjLock, error)`
- Produces:
  - `type finding struct{Check, Scope, Severity, Message, FixHint string; Fixable, Fixed bool}` (JSON-tagged)
  - consts `sevOK="ok"`, `sevWARN="warn"`, `sevERROR="error"`, `scopeMachine="machine"`, `scopeProject="project"`
  - `type docCtx struct{p config.Paths; projectDir string; lock ProjLock}`
  - `type check struct{name, scope string; diagnose func(docCtx) []finding; apply func(docCtx) error}`
  - `func resolveDocCtx(p config.Paths) docCtx`
  - `func runDoctor(dc docCtx, checks []check, doFix bool) []finding`
  - `func doctorExit(fs []finding) int`
  - `func doctorChecks() []check`
  - `func cmdDoctor(p config.Paths, args []string) error`

- [ ] **Step 1: Write the failing test**

```go
// cmd/smith/doctor_test.go
package main

import (
	"strings"
	"testing"
)

func TestRunDoctorReportsAndExits(t *testing.T) {
	p := testPaths(t) // isolates SMITH_HOME/SMITH_SKILLS_DIR to temp dirs
	// A check that is always ERROR and not fixable.
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/smith/ -run 'RunDoctor|RenderJSON' -v`
Expected: FAIL — `undefined: check`, `runDoctor`, etc.

- [ ] **Step 3: Write minimal implementation**

```go
// cmd/smith/doctor.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"smith/internal/config"
	"smith/internal/sandbox"
)

const (
	sevOK    = "ok"
	sevWARN  = "warn"
	sevERROR = "error"

	scopeMachine = "machine"
	scopeProject = "project"
)

type finding struct {
	Check    string `json:"check"`
	Scope    string `json:"scope"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	FixHint  string `json:"fixHint,omitempty"`
	Fixable  bool   `json:"fixable"`
	Fixed    bool   `json:"fixed"`
}

type docCtx struct {
	p          config.Paths
	projectDir string // "" if cwd is not a smith project
	lock       ProjLock
}

type check struct {
	name     string
	scope    string
	diagnose func(dc docCtx) []finding
	apply    func(dc docCtx) error // nil if not auto-fixable
}

// resolveDocCtx builds the context: machine always; project iff cwd has .smith/.
func resolveDocCtx(p config.Paths) docCtx {
	dc := docCtx{p: p}
	if fi, err := os.Stat(smithDir); err == nil && fi.IsDir() {
		dc.projectDir = "."
		dc.lock, _ = loadProjLock()
	}
	return dc
}

// runDoctor diagnoses each in-scope check; with doFix it applies fixable,
// non-OK findings then re-diagnoses to confirm.
func runDoctor(dc docCtx, checks []check, doFix bool) []finding {
	var out []finding
	for _, c := range checks {
		if c.scope == scopeProject && dc.projectDir == "" {
			continue
		}
		fs := c.diagnose(dc)
		if doFix && c.apply != nil && anyFixable(fs) {
			if err := c.apply(dc); err != nil {
				for i := range fs {
					if fs[i].Severity != sevOK {
						fs[i].Severity = sevERROR
						fs[i].Message += " (fix failed: " + err.Error() + ")"
					}
				}
			} else {
				fs = c.diagnose(dc)
				for i := range fs {
					fs[i].Fixed = true
				}
			}
		}
		out = append(out, fs...)
	}
	return out
}

func anyFixable(fs []finding) bool {
	for _, f := range fs {
		if f.Fixable && f.Severity != sevOK {
			return true
		}
	}
	return false
}

func doctorExit(fs []finding) int {
	for _, f := range fs {
		if f.Severity == sevERROR {
			return 1
		}
	}
	return 0
}

// renderDoctor returns the report as a string (human or json). Returning a
// string keeps it unit-testable; cmdDoctor prints it.
func renderDoctor(dc docCtx, fs []finding, asJSON bool) string {
	ok, warn, errc := 0, 0, 0
	for _, f := range fs {
		switch f.Severity {
		case sevOK:
			ok++
		case sevWARN:
			warn++
		case sevERROR:
			errc++
		}
	}
	if asJSON {
		type payload struct {
			Project  string         `json:"project,omitempty"`
			Findings []finding      `json:"findings"`
			Summary  map[string]int `json:"summary"`
		}
		b, _ := json.MarshalIndent(payload{
			Project:  dc.projectDir,
			Findings: fs,
			Summary:  map[string]int{"ok": ok, "warn": warn, "error": errc},
		}, "", "  ")
		return string(b)
	}
	glyph := map[string]string{sevOK: "✓", sevWARN: "⚠", sevERROR: "✗"}
	var b strings.Builder
	for _, sc := range []string{scopeMachine, scopeProject} {
		var lines []string
		for _, f := range fs {
			if f.Scope != sc {
				continue
			}
			line := "  " + glyph[f.Severity] + " " + f.Message
			if f.Fixed {
				line += "  (fixed)"
			} else if f.FixHint != "" && f.Severity != sevOK {
				line += "  → " + f.FixHint
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "%s\n%s\n", sc, strings.Join(lines, "\n"))
		}
	}
	fmt.Fprintf(&b, "\n%d ok · %d warnings · %d errors\n", ok, warn, errc)
	return b.String()
}

// doctorChecks is the full registry. Task 2 seeds it with the two simplest
// machine checks; later tasks append to it.
func doctorChecks() []check {
	return []check{
		{
			name: "mise-present", scope: scopeMachine,
			diagnose: func(dc docCtx) []finding {
				f := finding{Check: "mise-present", Scope: scopeMachine}
				if _, err := exec.LookPath("mise"); err != nil {
					f.Severity = sevERROR
					f.Message = "mise not found on PATH"
					f.FixHint = "install mise: https://mise.jdx.dev"
				} else {
					f.Severity = sevOK
					f.Message = "mise present"
				}
				return []finding{f}
			},
		},
		{
			name: "sandbox-functional", scope: scopeMachine,
			diagnose: func(dc docCtx) []finding {
				f := finding{Check: "sandbox-functional", Scope: scopeMachine}
				if err := sandbox.Functional(dc.p.SandboxProfiles); err != nil {
					f.Severity = sevERROR
					f.Message = "sandbox not functional: " + err.Error()
					f.FixHint = "adoption self-tests require a working sandbox"
				} else {
					f.Severity = sevOK
					f.Message = "sandbox functional"
				}
				return []finding{f}
			},
		},
	}
}

func cmdDoctor(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	doFix := fs.Bool("fix", false, "apply safe, reversible repairs")
	asJSON := fs.Bool("json", false, "machine-readable output")
	onlyMachine := fs.Bool("machine", false, "run machine checks only")
	onlyProject := fs.Bool("project", false, "run project checks only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dc := resolveDocCtx(p)
	checks := doctorChecks()
	if *onlyMachine || *onlyProject {
		want := scopeMachine
		if *onlyProject {
			want = scopeProject
		}
		var sel []check
		for _, c := range checks {
			if c.scope == want {
				sel = append(sel, c)
			}
		}
		checks = sel
	}
	results := runDoctor(dc, checks, *doFix)
	fmt.Println(renderDoctor(dc, results, *asJSON))
	os.Exit(doctorExit(results))
	return nil
}
```

Then wire dispatch and usage in `cmd/smith/main.go`:

```go
// in main()'s switch, after the "allowlist" case:
	case "doctor":
		err = cmdDoctor(paths, args)
```

```go
// in usage(), add to the command list (before "smith version"):
  smith doctor [--fix] [--json]   diagnose machine + project health; --fix repairs
                                 safe issues (activation, gitignore, v1 cruft)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./cmd/smith/ -run 'RunDoctor|RenderJSON' -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add cmd/smith/doctor.go cmd/smith/doctor_test.go cmd/smith/main.go
git commit -m "doctor: core registry, run loop, render, dispatch

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: v1-home-cruft check (refactor migrate into diagnose/apply)

**Files:**
- Modify: `cmd/smith/migrate.go` (extract `configStaleKeyNames`, `diagnoseV1Home`, `applyV1Migrate`; rewire `cmdMigrate` + `staleConfigKeys`)
- Modify: `cmd/smith/doctor.go` (append check to `doctorChecks`)
- Test: `cmd/smith/doctor_test.go`

**Interfaces:**
- Consumes: `v1HomeArtifacts []string`, `v2ConfigKeys map[string]bool`, `config.Paths.Home`
- Produces:
  - `func configStaleKeyNames(path string) ([]string, error)` — retired top-level keys, no write
  - `func diagnoseV1Home(p config.Paths) (present []string, staleKeys []string)`
  - `func applyV1Migrate(p config.Paths) (moved []string, dropped []string, backup string, err error)`

- [ ] **Step 1: Write the failing test**

```go
// cmd/smith/doctor_test.go  (add)
func TestV1CruftCheckDiagnosesAndFixes(t *testing.T) {
	p := testPaths(t)
	os.MkdirAll(filepath.Join(p.Home, "index"), 0o755)
	os.WriteFile(filepath.Join(p.Home, "config.yaml"),
		[]byte("transport:\n  repo: x/y\nreview:\n  default: tui\n"), 0o644)

	dc := docCtx{p: p}
	var c check
	for _, k := range doctorChecks() {
		if k.name == "v1-home-cruft" {
			c = k
		}
	}
	if c.name == "" {
		t.Fatal("v1-home-cruft check not registered")
	}
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
```

Add imports `os`, `path/filepath` to `doctor_test.go` if missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/smith/ -run TestV1CruftCheck -v`
Expected: FAIL — check not registered.

- [ ] **Step 3: Write minimal implementation**

Refactor `cmd/smith/migrate.go`. Replace `staleConfigKeys` with a pure name-finder plus a writer, and extract the mutation from `cmdMigrate`:

```go
// configStaleKeyNames returns retired top-level config keys without writing.
func configStaleKeyNames(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, nil // unparseable: leave alone
	}
	var dropped []string
	for k := range m {
		if !v2ConfigKeys[k] {
			dropped = append(dropped, k)
		}
	}
	sort.Strings(dropped)
	return dropped, nil
}

// staleConfigKeys strips retired keys from config.yaml, returning what it dropped.
func staleConfigKeys(path string) ([]string, error) {
	dropped, err := configStaleKeyNames(path)
	if err != nil || len(dropped) == 0 {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, nil
	}
	for _, k := range dropped {
		delete(m, k)
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return nil, err
	}
	return dropped, nil
}

// diagnoseV1Home reports retired v1 artifacts and dead config keys (no mutation).
func diagnoseV1Home(p config.Paths) (present []string, staleKeys []string) {
	for _, name := range v1HomeArtifacts {
		if _, err := os.Lstat(filepath.Join(p.Home, name)); err == nil {
			present = append(present, name)
		}
	}
	staleKeys, _ = configStaleKeyNames(filepath.Join(p.Home, "config.yaml"))
	return present, staleKeys
}

// applyV1Migrate backs up retired dirs and strips dead config keys.
func applyV1Migrate(p config.Paths) (moved []string, dropped []string, backup string, err error) {
	present, _ := diagnoseV1Home(p)
	if len(present) > 0 {
		backup = filepath.Join(p.Home, ".v1-backup-"+time.Now().Format("20060102"))
		if err = os.MkdirAll(backup, 0o755); err != nil {
			return
		}
		for _, name := range present {
			dst := filepath.Join(backup, name)
			if err = os.RemoveAll(dst); err != nil {
				return
			}
			if err = os.Rename(filepath.Join(p.Home, name), dst); err != nil {
				err = fmt.Errorf("backing up %s: %w", name, err)
				return
			}
			moved = append(moved, name)
		}
	}
	dropped, err = staleConfigKeys(filepath.Join(p.Home, "config.yaml"))
	return
}
```

Rewrite the body of `cmdMigrate` to call `applyV1Migrate` for the mutation (keep its `--quiet`/printing behavior):

```go
func cmdMigrate(p config.Paths, args []string) error {
	quiet := false
	for _, a := range args {
		if a == "--quiet" || a == "-q" {
			quiet = true
		}
	}
	say := func(format string, a ...any) {
		if !quiet {
			fmt.Printf(format+"\n", a...)
		}
	}
	present, staleKeys := diagnoseV1Home(p)
	if len(present) == 0 && len(staleKeys) == 0 {
		say("smith: already on the v2 layout; nothing to migrate")
		return nil
	}
	moved, dropped, backup, err := applyV1Migrate(p)
	if err != nil {
		return err
	}
	for _, name := range moved {
		say("  moved v1 %s → %s", name, backup)
	}
	if len(moved) > 0 {
		say("smith: backed up %d retired item(s) to %s (safe to delete once you're happy)", len(moved), backup)
	}
	if len(dropped) > 0 {
		say("  stripped retired config key(s): %s", strings.Join(dropped, ", "))
	}
	return nil
}
```

Confirm `migrate.go` still imports `time`, `sort`, `strings`, `fmt`, `os`, `path/filepath`, `gopkg.in/yaml.v3`, `smith/internal/config`.

Append the check in `doctorChecks()` (add to the returned slice):

```go
		{
			name: "v1-home-cruft", scope: scopeMachine,
			diagnose: func(dc docCtx) []finding {
				present, keys := diagnoseV1Home(dc.p)
				f := finding{Check: "v1-home-cruft", Scope: scopeMachine}
				if len(present) == 0 && len(keys) == 0 {
					f.Severity = sevOK
					f.Message = "no retired v1 files in SMITH_HOME"
				} else {
					f.Severity = sevWARN
					f.Fixable = true
					f.Message = fmt.Sprintf("retired v1 files in SMITH_HOME (%d dirs, %d dead config keys)", len(present), len(keys))
					f.FixHint = "smith doctor --fix"
				}
				return []finding{f}
			},
			apply: func(dc docCtx) error {
				_, _, _, err := applyV1Migrate(dc.p)
				return err
			},
		},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./cmd/smith/ -run 'V1CruftCheck|Migrate|StaleConfig' -v`
Expected: PASS (new check test + existing migrate tests unchanged).

- [ ] **Step 5: Commit**

```bash
git add cmd/smith/migrate.go cmd/smith/doctor.go cmd/smith/doctor_test.go
git commit -m "doctor: v1-home-cruft check; split migrate into diagnose/apply

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: activation check (refactor ensureActivation into state + apply)

**Files:**
- Modify: `cmd/smith/main.go` (add `activationState`; rewrite `ensureActivation` to switch on it)
- Modify: `cmd/smith/doctor.go` (append check)
- Test: `cmd/smith/doctor_test.go`

**Interfaces:**
- Consumes: `healStaleActivation(lines []string, newLine string) ([]string, bool)`, `smithDir`
- Produces: `func activationState(projectDir, binDir string) string` — one of `"ok"`,`"missing"`,`"stale"`,`"foreign"`,`"inject"`.

- [ ] **Step 1: Write the failing test**

```go
// cmd/smith/doctor_test.go  (add)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/smith/ -run TestActivationState -v`
Expected: FAIL — `undefined: activationState`.

- [ ] **Step 3: Write minimal implementation**

Add to `cmd/smith/main.go`:

```go
// activationState classifies mise.local.toml without mutating it:
//   ok      – already activates binDir
//   missing – no file (create one)
//   stale   – a smith-managed _.path pointing elsewhere (heal it)
//   foreign – a user-authored _.path (do not touch)
//   inject  – file exists with no _.path (add one under [env])
func activationState(projectDir, binDir string) string {
	raw, err := os.ReadFile(filepath.Join(projectDir, "mise.local.toml"))
	if os.IsNotExist(err) {
		return "missing"
	}
	if err != nil {
		return "foreign" // unreadable: refuse to touch
	}
	text := string(raw)
	if strings.Contains(text, binDir) {
		return "ok"
	}
	if _, ok := healStaleActivation(strings.Split(text, "\n"), ""); ok {
		return "stale"
	}
	if strings.Contains(text, "_.path") {
		return "foreign"
	}
	return "inject"
}
```

Rewrite `ensureActivation` (same file) to switch on the state — preserving current behavior:

```go
func ensureActivation(projectDir, binDir string) error {
	wantLine := fmt.Sprintf("_.path = [%q]", binDir)
	path := filepath.Join(projectDir, "mise.local.toml")
	switch activationState(projectDir, binDir) {
	case "ok":
		return nil
	case "missing":
		return os.WriteFile(path, []byte("# managed by smith: puts adopted tools on PATH\n[env]\n"+wantLine+"\n"), 0o644)
	case "stale":
		raw, _ := os.ReadFile(path)
		lines, _ := healStaleActivation(strings.Split(string(raw), "\n"), wantLine)
		fmt.Fprintf(os.Stderr, "[smith] healed stale activation in mise.local.toml → %s\n", binDir)
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	case "foreign":
		return fmt.Errorf("mise.local.toml already sets env._.path; add %q to it manually", binDir)
	default: // inject
		raw, _ := os.ReadFile(path)
		text := string(raw)
		if i := strings.Index(text, "[env]"); i >= 0 {
			at := i + len("[env]")
			text = text[:at] + "\n" + wantLine + text[at:]
		} else {
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += "\n[env]\n" + wantLine + "\n"
		}
		return os.WriteFile(path, []byte(text), 0o644)
	}
}
```

Append the check in `doctorChecks()`:

```go
		{
			name: "activation", scope: scopeProject,
			diagnose: func(dc docCtx) []finding {
				binDir, _ := filepathAbs(filepathJoin(dc.projectDir, smithDir, "bin"))
				f := finding{Check: "activation", Scope: scopeProject}
				switch activationState(dc.projectDir, binDir) {
				case "ok":
					f.Severity = sevOK
					f.Message = "activation → .smith/bin"
				case "foreign":
					f.Severity = sevERROR
					f.Message = "mise.local.toml has a non-smith _.path"
					f.FixHint = "edit mise.local.toml to add " + binDir
				default:
					f.Severity = sevERROR
					f.Fixable = true
					f.Message = "mise.local.toml does not activate .smith/bin"
					f.FixHint = "smith doctor --fix"
				}
				return []finding{f}
			},
			apply: func(dc docCtx) error {
				binDir, err := filepathAbs(filepathJoin(dc.projectDir, smithDir, "bin"))
				if err != nil {
					return err
				}
				return ensureActivation(dc.projectDir, binDir)
			},
		},
```

Add these tiny aliases at the top of `doctor.go` (or import `path/filepath` and use it directly — pick one and use consistently):

```go
// at top of doctor.go, replace the alias approach by importing path/filepath
// and calling filepath.Abs / filepath.Join directly.
```

> Implementation note: do **not** introduce `filepathAbs`/`filepathJoin` helpers. Import `path/filepath` in `doctor.go` and call `filepath.Abs`/`filepath.Join`. The pseudo-names above are only to show intent.

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./cmd/smith/ -run 'ActivationState|EnsureActivation|Adopt|UseInstall' -v`
Expected: PASS — new state test plus existing activation/adoption tests (behavior unchanged).

- [ ] **Step 5: Commit**

```bash
git add cmd/smith/main.go cmd/smith/doctor.go cmd/smith/doctor_test.go
git commit -m "doctor: activation check; factor activationState out of ensureActivation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: gitignore + tracked-mise-local checks

**Files:**
- Modify: `cmd/smith/adopt.go` (add `gitignoreMissing`; rewrite `ensureProjectGitignore` to use it)
- Modify: `cmd/smith/doctor.go` (append two checks + `miseLocalTracked`)
- Test: `cmd/smith/doctor_test.go`

**Interfaces:**
- Produces:
  - `func gitignoreMissing() []string` — required ignore entries not present in `./.gitignore`
  - `func miseLocalTracked() bool` — true iff `mise.local.toml` is git-tracked in cwd

- [ ] **Step 1: Write the failing test**

```go
// cmd/smith/doctor_test.go  (add)
func TestGitignoreCheck(t *testing.T) {
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)
	os.MkdirAll(smithDir, 0o755) // makes cwd a "project"

	var c check
	for _, k := range doctorChecks() {
		if k.name == "gitignore" {
			c = k
		}
	}
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/smith/ -run TestGitignoreCheck -v`
Expected: FAIL — check not registered.

- [ ] **Step 3: Write minimal implementation**

In `cmd/smith/adopt.go`, add `gitignoreMissing` and rewrite `ensureProjectGitignore`:

```go
// gitignoreMissing returns the required ignore entries not present in ./.gitignore.
func gitignoreMissing() []string {
	raw, _ := os.ReadFile(".gitignore")
	content := string(raw)
	has := func(want string) bool {
		for _, l := range strings.Split(content, "\n") {
			if strings.TrimSpace(l) == want {
				return true
			}
		}
		return false
	}
	var missing []string
	for _, want := range []string{smithDir + "/bin/", smithDir + "/cache/", "mise.local.toml"} {
		if !has(want) {
			missing = append(missing, want)
		}
	}
	return missing
}

func ensureProjectGitignore() {
	missing := gitignoreMissing()
	if len(missing) == 0 {
		return
	}
	raw, _ := os.ReadFile(".gitignore")
	content := string(raw)
	for _, want := range missing {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += want + "\n"
	}
	_ = os.WriteFile(".gitignore", []byte(content), 0o644)
}
```

In `cmd/smith/doctor.go`, add `miseLocalTracked` and append the two checks:

```go
// miseLocalTracked reports whether mise.local.toml is tracked by git in cwd.
func miseLocalTracked() bool {
	return exec.Command("git", "ls-files", "--error-unmatch", "mise.local.toml").Run() == nil
}
```

```go
		{
			name: "gitignore", scope: scopeProject,
			diagnose: func(dc docCtx) []finding {
				missing := gitignoreMissing()
				f := finding{Check: "gitignore", Scope: scopeProject}
				if len(missing) == 0 {
					f.Severity = sevOK
					f.Message = ".gitignore covers generated smith paths"
				} else {
					f.Severity = sevWARN
					f.Fixable = true
					f.Message = ".gitignore missing: " + strings.Join(missing, ", ")
					f.FixHint = "smith doctor --fix"
				}
				return []finding{f}
			},
			apply: func(dc docCtx) error { ensureProjectGitignore(); return nil },
		},
		{
			name: "tracked-mise-local", scope: scopeProject,
			diagnose: func(dc docCtx) []finding {
				f := finding{Check: "tracked-mise-local", Scope: scopeProject}
				if miseLocalTracked() {
					f.Severity = sevWARN
					f.Message = "mise.local.toml is git-tracked (ships a machine-absolute path)"
					f.FixHint = "git rm --cached mise.local.toml"
				} else {
					f.Severity = sevOK
					f.Message = "mise.local.toml not tracked"
				}
				return []finding{f}
			},
			// no apply: doctor --fix never runs git (git boundary)
		},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./cmd/smith/ -run 'GitignoreCheck|Gitignore|Adopt' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/smith/adopt.go cmd/smith/doctor.go cmd/smith/doctor_test.go
git commit -m "doctor: gitignore + tracked-mise-local checks

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: vendored-sha check

**Files:**
- Modify: `cmd/smith/doctor.go` (append check + `sortedToolNames` helper)
- Test: `cmd/smith/doctor_test.go`

**Interfaces:**
- Consumes: `bundleHash(dir string, files []string) (string, error)`, `sourceFiles(runtime, entrypoint, name string) []string`, `ProjLock`, `ProjTool`
- Produces: `func sortedToolNames(l ProjLock) []string`

- [ ] **Step 1: Write the failing test**

```go
// cmd/smith/doctor_test.go  (add)
func TestVendoredShaCheckDetectsDrift(t *testing.T) {
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)

	tdir := filepath.Join(smithDir, "tools", "greet")
	os.MkdirAll(tdir, 0o755)
	os.WriteFile(filepath.Join(tdir, "greet"), []byte("#!/bin/bash\necho hi\n"), 0o755)
	os.WriteFile(filepath.Join(tdir, "manifest.yaml"), []byte("name: greet\n"), 0o644)
	os.WriteFile(filepath.Join(tdir, "selftest.sh"), []byte("echo ok\n"), 0o644)
	os.WriteFile(filepath.Join(tdir, "SKILL.md"), []byte("# greet\n"), 0o644)
	good, _ := bundleHash(tdir, sourceFiles("bash", "greet", "greet"))

	lock := ProjLock{Tools: map[string]ProjTool{
		"greet": {Source: "x/greet", Version: "v1", Sha256: good, Entrypoint: "greet", Runtime: "bash"},
	}}
	dc := docCtx{projectDir: ".", lock: lock}

	var c check
	for _, k := range doctorChecks() {
		if k.name == "vendored-sha" {
			c = k
		}
	}
	if fs := c.diagnose(dc); fs[0].Severity != sevOK {
		t.Fatalf("matching sha → OK, got %+v", fs[0])
	}
	// Tamper: change a vendored file → sha drifts.
	os.WriteFile(filepath.Join(tdir, "greet"), []byte("#!/bin/bash\necho tampered\n"), 0o755)
	if fs := c.diagnose(dc); fs[0].Severity != sevERROR {
		t.Fatalf("drift → ERROR, got %+v", fs[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/smith/ -run TestVendoredShaCheck -v`
Expected: FAIL — check not registered.

- [ ] **Step 3: Write minimal implementation**

Append to `cmd/smith/doctor.go`:

```go
func sortedToolNames(l ProjLock) []string {
	names := make([]string, 0, len(l.Tools))
	for n := range l.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
```

(Add `"sort"` to `doctor.go` imports.)

```go
		{
			name: "vendored-sha", scope: scopeProject,
			diagnose: func(dc docCtx) []finding {
				names := sortedToolNames(dc.lock)
				if len(names) == 0 {
					return []finding{{Check: "vendored-sha", Scope: scopeProject, Severity: sevOK, Message: "no tools pinned"}}
				}
				var out []finding
				for _, name := range names {
					t := dc.lock.Tools[name]
					dir := filepath.Join(dc.projectDir, smithDir, "tools", name)
					f := finding{Check: "vendored-sha", Scope: scopeProject}
					sum, err := bundleHash(dir, sourceFiles(t.Runtime, t.Entrypoint, name))
					switch {
					case err != nil:
						f.Severity = sevERROR
						f.Message = name + ": cannot hash vendored source: " + err.Error()
					case sum != t.Sha256:
						f.Severity = sevERROR
						f.Message = name + ": vendored source sha ≠ pin"
						f.FixHint = "smith use " + t.Source + "@" + t.Version + "  (re-vendor) or restore from git"
					default:
						f.Severity = sevOK
						f.Message = name + ": vendored source matches pin"
					}
					out = append(out, f)
				}
				return out
			},
			// no apply: re-vendoring needs a network clone (outside --fix)
		},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./cmd/smith/ -run TestVendoredShaCheck -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/smith/doctor.go cmd/smith/doctor_test.go
git commit -m "doctor: vendored-sha drift check

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: installed check (extract materializeTool from cmdInstall)

**Files:**
- Modify: `cmd/smith/adopt.go` (extract `materializeTool`; call it from `cmdInstall`)
- Modify: `cmd/smith/doctor.go` (append check)
- Test: `cmd/smith/doctor_test.go`

**Interfaces:**
- Produces: `func materializeTool(p config.Paths, name string, t ProjTool, binDir, absBinDir string) error` — builds/symlinks/launches one tool onto binDir (the per-tool body of `cmdInstall`, after the sha check).

- [ ] **Step 1: Write the failing test**

```go
// cmd/smith/doctor_test.go  (add)
func TestInstalledCheckDetectsMissing(t *testing.T) {
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)

	tdir := filepath.Join(smithDir, "tools", "greet")
	os.MkdirAll(tdir, 0o755)
	os.WriteFile(filepath.Join(tdir, "greet"), []byte("#!/bin/bash\necho hi\n"), 0o755)
	lock := ProjLock{Tools: map[string]ProjTool{
		"greet": {Source: "x/greet", Version: "v1", Entrypoint: "greet", Runtime: "bash"},
	}}
	dc := docCtx{projectDir: ".", lock: lock}

	var c check
	for _, k := range doctorChecks() {
		if k.name == "installed" {
			c = k
		}
	}
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/smith/ -run TestInstalledCheck -v`
Expected: FAIL — check not registered.

- [ ] **Step 3: Write minimal implementation**

In `cmd/smith/adopt.go`, extract the per-tool materialize body (the `switch t.Runtime` block currently inside `cmdInstall`) into a function, and call it from `cmdInstall`:

```go
// materializeTool realizes one tool onto binDir per its runtime: go builds the
// vendored source, python-uv warms a cache + writes an offline launcher, and
// bash/python symlink the entrypoint. binDir is the relative .smith/bin;
// absBinDir is its absolute form (used for go build output).
func materializeTool(p config.Paths, name string, t ProjTool, binDir, absBinDir string) error {
	dir := filepath.Join(smithDir, "tools", name)
	link := filepath.Join(binDir, name)
	_ = os.Remove(link)
	switch t.Runtime {
	case "go":
		if err := buildGoTool(dir, filepath.Join(absBinDir, name)); err != nil {
			return fmt.Errorf("%s: go build failed: %w", name, err)
		}
	case "python-uv":
		if _, err := exec.LookPath("uv"); err != nil {
			return fmt.Errorf("%s: uv not found on PATH", name)
		}
		scriptAbs, _ := filepath.Abs(filepath.Join(dir, name+".py"))
		cacheAbs, _ := filepath.Abs(filepath.Join(smithDir, "cache", name))
		if err := os.MkdirAll(cacheAbs, 0o755); err != nil {
			return err
		}
		cwd, _ := os.Getwd()
		if err := warmUVCache(p, cwd, scriptAbs, cacheAbs); err != nil {
			return fmt.Errorf("%s: warming uv cache failed: %w", name, err)
		}
		if err := os.WriteFile(link, []byte(pythonUVLauncher(scriptAbs, cacheAbs)), 0o755); err != nil {
			return err
		}
	default: // bash / python
		if err := os.Chmod(filepath.Join(dir, t.Entrypoint), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(filepath.Join("..", "tools", name, t.Entrypoint), link); err != nil {
			return err
		}
	}
	return nil
}
```

Replace the inline `switch t.Runtime { ... }` in `cmdInstall` (the block from `link := filepath.Join(binDir, name)` through the end of the switch) with:

```go
		if err := materializeTool(p, name, t, binDir, absBinEarly); err != nil {
			return err
		}
```

(The sha-mismatch check stays before this call, unchanged.)

Append the check in `doctorChecks()`:

```go
		{
			name: "installed", scope: scopeProject,
			diagnose: func(dc docCtx) []finding {
				names := sortedToolNames(dc.lock)
				if len(names) == 0 {
					return []finding{{Check: "installed", Scope: scopeProject, Severity: sevOK, Message: "no tools pinned"}}
				}
				binDir := filepath.Join(dc.projectDir, smithDir, "bin")
				var out []finding
				for _, name := range names {
					f := finding{Check: "installed", Scope: scopeProject}
					if _, err := os.Lstat(filepath.Join(binDir, name)); err == nil {
						f.Severity = sevOK
						f.Message = name + ": on PATH"
					} else {
						f.Severity = sevWARN
						f.Fixable = true
						f.Message = name + ": pinned but not installed"
						f.FixHint = "smith install"
					}
					out = append(out, f)
				}
				return out
			},
			apply: func(dc docCtx) error {
				binDir := filepath.Join(dc.projectDir, smithDir, "bin")
				absBin, err := filepath.Abs(binDir)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(binDir, 0o755); err != nil {
					return err
				}
				for _, name := range sortedToolNames(dc.lock) {
					if _, err := os.Lstat(filepath.Join(binDir, name)); err == nil {
						continue
					}
					if err := materializeTool(dc.p, name, dc.lock.Tools[name], binDir, absBin); err != nil {
						return err
					}
				}
				return nil
			},
		},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./cmd/smith/ -run 'InstalledCheck|UseInstall|Install' -v`
Expected: PASS — new check plus existing install/adoption tests (behavior unchanged).

- [ ] **Step 5: Commit**

```bash
git add cmd/smith/adopt.go cmd/smith/doctor.go cmd/smith/doctor_test.go
git commit -m "doctor: installed check; extract materializeTool from cmdInstall

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: skill-trigger check

Flag a vendored tool whose `SKILL.md` frontmatter `description` (the agent
trigger) is empty or still the scaffold placeholder. Report-only — writing a
good trigger is human judgment.

**Files:**
- Modify: `cmd/smith/doctor.go` (add `skillTriggerIssue` + append check; import `gopkg.in/yaml.v3`)
- Test: `cmd/smith/doctor_test.go`

**Interfaces:**
- Consumes: `sortedToolNames(l ProjLock) []string`, `ProjLock`, `smithDir`
- Produces: `func skillTriggerIssue(skillPath string) (reason string, bad bool)`

- [ ] **Step 1: Write the failing test**

```go
// cmd/smith/doctor_test.go  (add)
func TestSkillTriggerCheck(t *testing.T) {
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)
	tdir := filepath.Join(smithDir, "tools", "greet")
	os.MkdirAll(tdir, 0o755)
	lock := ProjLock{Tools: map[string]ProjTool{"greet": {Entrypoint: "greet", Runtime: "bash"}}}
	dc := docCtx{projectDir: ".", lock: lock}

	var c check
	for _, k := range doctorChecks() {
		if k.name == "skill-trigger" {
			c = k
		}
	}
	if c.name == "" {
		t.Fatal("skill-trigger check not registered")
	}

	// Placeholder trigger → WARN.
	os.WriteFile(filepath.Join(tdir, "SKILL.md"),
		[]byte("---\nname: greet\ndescription: print a greeting. TODO: sharpen the trigger — name the situations\n---\n# greet\n"), 0o644)
	if fs := c.diagnose(dc); fs[0].Severity != sevWARN {
		t.Fatalf("placeholder → WARN, got %+v", fs[0])
	}

	// Sharpened trigger → OK.
	os.WriteFile(filepath.Join(tdir, "SKILL.md"),
		[]byte("---\nname: greet\ndescription: Print a greeting; use when a session needs a canned hello line.\n---\n# greet\n"), 0o644)
	if fs := c.diagnose(dc); fs[0].Severity != sevOK {
		t.Fatalf("sharpened → OK, got %+v", fs[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/smith/ -run TestSkillTriggerCheck -v`
Expected: FAIL — check not registered.

- [ ] **Step 3: Write minimal implementation**

Add `"gopkg.in/yaml.v3"` to `doctor.go` imports, then:

```go
// skillTriggerIssue reports whether a vendored SKILL.md has an unusable agent
// trigger — a missing/empty frontmatter description, or the scaffold placeholder.
// It cannot judge whether a trigger is *complete* (that a new command is
// mentioned) — that's the cli-creator "Changing a tool" checklist's job.
func skillTriggerIssue(skillPath string) (reason string, bad bool) {
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		return "SKILL.md missing", true
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---") {
		return "SKILL.md has no frontmatter", true
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "SKILL.md frontmatter not closed", true
	}
	var meta struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &meta); err != nil {
		return "SKILL.md frontmatter unparseable", true
	}
	d := strings.TrimSpace(meta.Description)
	if d == "" {
		return "empty SKILL.md trigger", true
	}
	if strings.Contains(d, "TODO: sharpen the trigger") {
		return "placeholder SKILL.md trigger — sharpen it", true
	}
	return "", false
}
```

Append the check in `doctorChecks()`:

```go
		{
			name: "skill-trigger", scope: scopeProject,
			diagnose: func(dc docCtx) []finding {
				names := sortedToolNames(dc.lock)
				if len(names) == 0 {
					return []finding{{Check: "skill-trigger", Scope: scopeProject, Severity: sevOK, Message: "no tools pinned"}}
				}
				var out []finding
				for _, name := range names {
					f := finding{Check: "skill-trigger", Scope: scopeProject}
					path := filepath.Join(dc.projectDir, smithDir, "tools", name, "SKILL.md")
					if reason, bad := skillTriggerIssue(path); bad {
						f.Severity = sevWARN
						f.Message = name + ": " + reason
						f.FixHint = "sharpen the SKILL.md frontmatter description in the tool repo, release a new version, re-adopt"
					} else {
						f.Severity = sevOK
						f.Message = name + ": trigger looks sharpened"
					}
					out = append(out, f)
				}
				return out
			},
			// no apply: writing a good trigger is human judgment
		},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./cmd/smith/ -run TestSkillTriggerCheck -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/smith/doctor.go cmd/smith/doctor_test.go
git commit -m "doctor: skill-trigger check (empty/placeholder SKILL trigger)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: End-to-end integration test + docs

**Files:**
- Test: `cmd/smith/doctor_test.go` (integration)
- Modify: `README.md` (command list + a short doctor section)
- Modify: `CLAUDE.md` (note doctor)

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Write the failing integration test**

```go
// cmd/smith/doctor_test.go  (add)
func TestDoctorEndToEndProjectFix(t *testing.T) {
	requireSandbox(t) // sandbox-functional is a machine check
	p := testPaths(t)
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)

	// Seed a project with a pinned+vendored bash tool but no bin/ or activation.
	tdir := filepath.Join(smithDir, "tools", "greet")
	os.MkdirAll(tdir, 0o755)
	os.WriteFile(filepath.Join(tdir, "greet"), []byte("#!/bin/bash\necho hi\n"), 0o755)
	os.WriteFile(filepath.Join(tdir, "manifest.yaml"), []byte("name: greet\n"), 0o644)
	os.WriteFile(filepath.Join(tdir, "selftest.sh"), []byte("echo ok\n"), 0o644)
	os.WriteFile(filepath.Join(tdir, "SKILL.md"), []byte("# greet\n"), 0o644)
	sum, _ := bundleHash(tdir, sourceFiles("bash", "greet", "greet"))
	saveProjLock(ProjLock{Tools: map[string]ProjTool{
		"greet": {Source: "x/greet", Version: "v1", Sha256: sum, Entrypoint: "greet", Runtime: "bash"},
	}})

	dc := resolveDocCtx(p)
	if dc.projectDir == "" {
		t.Fatal("expected project context")
	}
	// Report finds activation + installed issues (both fixable).
	before := runDoctor(dc, doctorChecks(), false)
	if doctorExit(before) == 0 {
		t.Fatalf("expected issues before fix, got %+v", before)
	}
	// Fix them.
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
```

- [ ] **Step 2: Run test to verify it fails/passes**

Run: `mise exec -- go test ./cmd/smith/ -run TestDoctorEndToEnd -v`
Expected: PASS (all checks implemented by now). If it fails, the failure pinpoints the check to fix.

- [ ] **Step 3: Update docs**

In `README.md`, add `smith doctor` to the command list and a short section:

```markdown
### smith doctor

Diagnose project + machine health:

    smith doctor            # report only (safe, CI-friendly; exits non-zero on errors)
    smith doctor --fix      # apply safe, reversible repairs
    smith doctor --json     # machine-readable

Checks: mise present, sandbox functional, retired v1 files (fixable); and, in a
project, activation, .gitignore coverage, a git-tracked mise.local.toml,
vendored-source drift, and pinned-but-not-installed tools. `--fix` repairs
activation, .gitignore, install, and v1 cruft; it never runs git and never
re-fetches over the network.
```

In `CLAUDE.md`, under the commands note, add:

```markdown
- `smith doctor` is the proactive health command: it consolidates the reactive
  heal/verify logic (activation, gitignore, vendored-sha, sandbox probe, v1
  cruft) behind one command. When adding a health check, add it to the
  `doctorChecks()` registry as a diagnose/apply pair — don't duplicate detection.
```

- [ ] **Step 4: Run the whole suite + vet**

Run: `mise exec -- go vet ./... && mise exec -- go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/smith/doctor_test.go README.md CLAUDE.md
git commit -m "doctor: end-to-end test + docs

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Fix model (report / `--fix`) → Task 2 run loop. ✓
- Context-aware scope → `resolveDocCtx` + scope filter, Task 2. ✓
- Output + `--json` + exit codes → `renderDoctor` + `doctorExit`, Task 2. ✓
- Check registry over diagnose/apply → Tasks 2-7. ✓
- Machine checks (mise-present, sandbox-functional, v1-home-cruft) → Tasks 2, 3. ✓
- Project checks (activation, gitignore, tracked-mise-local, vendored-sha, installed, skill-trigger) → Tasks 4-8. ✓
- Git boundary (no git in `--fix`) → tracked-mise-local has no `apply`. ✓
- Refactors share one source of truth → activationState, gitignoreMissing, diagnoseV1Home, materializeTool, sandbox.Functional. ✓
- Coupling checklist (dispatch, usage, README, CLAUDE.md) → Tasks 2, 9. ✓
- skill-trigger check (empty/placeholder SKILL trigger) → Task 8. ✓
- **Deviation:** the spec listed a `mise-trust` project check. It is **dropped** from v1: there is no reliable read-only signal for "is this dir trusted", and `mise trust` already runs during `smith install`. Revisit if mise exposes a query. (Recorded here so it is not silently missing.)

**Placeholder scan:** No TBD/TODO. The only pseudo-code (`filepathAbs`/`filepathJoin` in Task 4) is explicitly flagged as intent with an instruction to use `path/filepath` directly.

**Type consistency:** `finding`, `check`, `docCtx` fields and the `sevOK/sevWARN/sevERROR`, `scopeMachine/scopeProject` consts are used identically across tasks. `materializeTool`, `activationState`, `gitignoreMissing`, `diagnoseV1Home`, `applyV1Migrate`, `configStaleKeyNames`, `sortedToolNames`, `miseLocalTracked`, `sandbox.Functional` signatures match every call site.

## Rollout

After merge, ship as **v2.6.0** (new command, no breaking changes): tag, `make cross`, `gh release create` with the four binaries + `cli-creator-SKILL.md`.
