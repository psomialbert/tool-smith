package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	lockErr    error // non-nil if .smith/tools.yaml exists but won't parse
}

// check is one health check: diagnose reports findings; apply (if non-nil)
// repairs the fixable ones. Detection and repair share the same underlying
// helpers as install/use/migrate — one source of truth per check.
type check struct {
	name     string
	scope    string
	diagnose func(dc docCtx) []finding
	apply    func(dc docCtx) error
}

// resolveDocCtx builds the context: machine always; project iff cwd has .smith/.
func resolveDocCtx(p config.Paths) docCtx {
	dc := docCtx{p: p}
	if fi, err := os.Stat(smithDir); err == nil && fi.IsDir() {
		dc.projectDir = "."
		dc.lock, dc.lockErr = loadProjLock()
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
				// Re-diagnose to confirm. Mark "(fixed)" only where a finding
				// actually transitioned non-OK -> OK: a repair that silently
				// didn't take stays visibly non-OK, and a finding that was OK
				// all along isn't relabelled. diagnose is deterministic over
				// the same context, so index i is the same subject before/after.
				before := fs
				fs = c.diagnose(dc)
				for i := range fs {
					if fs[i].Severity == sevOK && i < len(before) && before[i].Severity != sevOK {
						fs[i].Fixed = true
					}
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

// renderDoctor returns the report as a string (human or json), keeping it
// unit-testable; cmdDoctor prints it.
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

// sortedToolNames returns the pinned tool names in stable order.
func sortedToolNames(l ProjLock) []string {
	names := make([]string, 0, len(l.Tools))
	for n := range l.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// miseLocalTracked reports whether mise.local.toml is tracked by git in cwd.
func miseLocalTracked() bool {
	return exec.Command("git", "ls-files", "--error-unmatch", "mise.local.toml").Run() == nil
}

// skillTriggerIssue reports whether a vendored SKILL.md has an unusable agent
// trigger — a missing/empty frontmatter description, or the scaffold
// placeholder. It cannot judge whether a trigger is *complete* (that a new
// command is mentioned); that's the cli-creator "Changing a tool" checklist.
//
// It line-scans for the description rather than strict-YAML-parsing the
// frontmatter on purpose: these triggers are prose that legitimately contains
// ": " and quotes — valid to the Claude harness but rejected by a strict YAML
// plain-scalar parse. We only care whether a real trigger is present.
func skillTriggerIssue(skillPath string) (reason string, bad bool) {
	desc := skillDescription(skillPath)
	if desc == "" {
		return "empty or missing SKILL.md trigger", true
	}
	if strings.Contains(desc, "TODO: sharpen the trigger") {
		return "placeholder SKILL.md trigger — sharpen it", true
	}
	return "", false
}

// skillDescription returns the frontmatter `description:` (the agent trigger)
// from a SKILL.md, or "" if the file/frontmatter/key is absent or empty. It
// line-scans rather than strict-YAML-parsing: these triggers are prose that
// legitimately contains ": " and quotes — valid to the Claude harness but
// rejected by a strict YAML plain-scalar parse. Shared by the doctor
// skill-trigger check and `smith lens`.
func skillDescription(skillPath string) string {
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		return ""
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---") {
		return ""
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	for _, ln := range strings.Split(rest[:end], "\n") {
		s := strings.TrimSpace(ln)
		if strings.HasPrefix(s, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(s, "description:"))
		}
	}
	return ""
}

// doctorChecks is the full registry, in report order.
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
		{
			name: "lockfile", scope: scopeProject,
			diagnose: func(dc docCtx) []finding {
				f := finding{Check: "lockfile", Scope: scopeProject}
				if dc.lockErr != nil {
					f.Severity = sevERROR
					f.Message = ".smith/tools.yaml does not parse: " + dc.lockErr.Error()
					f.FixHint = "fix the YAML by hand (a bad merge?) or restore it from git"
				} else {
					f.Severity = sevOK
					f.Message = ".smith/tools.yaml parses"
				}
				return []finding{f}
			},
			// no apply: hand-editing YAML is human judgment
		},
		{
			name: "activation", scope: scopeProject,
			diagnose: func(dc docCtx) []finding {
				binDir, _ := filepath.Abs(filepath.Join(dc.projectDir, smithDir, "bin"))
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
				binDir, err := filepath.Abs(filepath.Join(dc.projectDir, smithDir, "bin"))
				if err != nil {
					return err
				}
				return ensureActivation(dc.projectDir, binDir)
			},
		},
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
					if t.isMise() {
						continue // external tool: no vendored source to hash
					}
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
					if dc.lock.Tools[name].isMise() {
						continue // external tool: on PATH via mise, not .smith/bin
					}
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
					t := dc.lock.Tools[name]
					if t.isMise() {
						continue // external: fetched via mise, not materialized
					}
					if _, err := os.Lstat(filepath.Join(binDir, name)); err == nil {
						continue
					}
					// Same integrity gate as `smith install`: never build/put on
					// PATH a vendored tree that no longer matches its pin.
					dir := filepath.Join(dc.projectDir, smithDir, "tools", name)
					src := sourceFiles(t.Runtime, t.Entrypoint, name)
					sum, herr := bundleHash(dir, src)
					if herr != nil {
						return fmt.Errorf("%s: %w", name, herr)
					}
					if sum != t.Sha256 {
						return fmt.Errorf("%s: vendored source sha mismatch (pinned %s, got %s) — review %s and re-adopt", name, shortSha(t.Sha256), shortSha(sum), dir)
					}
					if extra := unexpectedFiles(dir, src); len(extra) > 0 {
						return fmt.Errorf("%s: un-pinned files in vendored dir: %s — review %s and re-adopt", name, strings.Join(extra, ", "), dir)
					}
					if err := materializeTool(dc.p, name, t, binDir, absBin); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			name: "skill-trigger", scope: scopeProject,
			diagnose: func(dc docCtx) []finding {
				names := sortedToolNames(dc.lock)
				if len(names) == 0 {
					return []finding{{Check: "skill-trigger", Scope: scopeProject, Severity: sevOK, Message: "no tools pinned"}}
				}
				var out []finding
				for _, name := range names {
					if dc.lock.Tools[name].isMise() {
						continue // external tool: no vendored SKILL.md by design
					}
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
