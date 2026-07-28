// Package scaffold generates the draft workspace for an agent-authored tool
// (`smith new`): tool source from a runtime template, a self-test, a SKILL.md
// discovery stub, and a draft manifest. Templates are embedded so smith stays
// a single binary.
package scaffold

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// toolNameRe matches a safe tool name: a single path element that can serve as
// a directory and skill name — starts alphanumeric, then [A-Za-z0-9._-]. It
// admits no path separator and (starting alphanumeric) no "."/".." so it can't
// traverse when joined into a filesystem path.
var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidToolName reports whether name is safe to use as a vendored-tool
// directory and skill name. The adoption path feeds an untrusted manifest's
// name straight into filesystem paths, so this is a security boundary, not a
// style check.
func ValidToolName(name string) bool { return toolNameRe.MatchString(name) }

//go:embed templates/*
var templates embed.FS

var Runtimes = []string{"bash", "python", "go", "python-uv"}

// SourceFile returns the draft source filename for a compiled/wrapped runtime
// (the file that isn't the executable entrypoint), or "" for interpreted
// runtimes whose entrypoint IS the source.
func SourceFile(m Manifest) string {
	switch m.Runtime {
	case "go":
		return "main.go"
	case "python-uv":
		return m.Name + ".py"
	}
	return ""
}

// Manifest is a tool's manifest.yaml — its runtime metadata. Description is
// used only to render {{DESC}} into the scaffold at creation time; it is NOT
// serialized to manifest.yaml. The tool's one description lives in the SKILL.md
// frontmatter (the agent trigger, the only description smith surfaces), so there
// is a single source of truth that can't drift.
type Manifest struct {
	Name        string       `yaml:"name"`
	Version     string       `yaml:"version"`
	Description string       `yaml:"-"` // render-time only; the SKILL.md frontmatter is the canonical description
	Runtime     string       `yaml:"runtime"`
	Entrypoint  string       `yaml:"entrypoint"`        // file in the draft dir that becomes bin/<name>
	Sandbox     *SandboxSpec `yaml:"sandbox,omitempty"` // per-tool grants beyond the strict default
}

// SandboxSpec is the tool's requested sandbox relaxations (its declared
// capabilities). Absent = the strict default (no network, writes confined to
// CWD+TMP). Every grant here is surfaced at adoption and enforced during the
// sandboxed self-test — a tool asking for network or extra write paths is a
// flag to scrutinize in review.
type SandboxSpec struct {
	Network bool     `yaml:"network,omitempty"` // allow network egress
	Write   []string `yaml:"write,omitempty"`   // extra writable paths beyond CWD+TMP (a leading ~ expands to $HOME)
}

func render(tpl string, m Manifest) (string, error) {
	raw, err := templates.ReadFile("templates/" + tpl)
	if err != nil {
		return "", err
	}
	s := string(raw)
	s = strings.ReplaceAll(s, "{{NAME}}", m.Name)
	s = strings.ReplaceAll(s, "{{DESC}}", m.Description)
	s = strings.ReplaceAll(s, "{{VERSION}}", m.Version)
	return s, nil
}

// Create writes a fresh draft into dir. Refuses to touch an existing draft.
func Create(dir string, m Manifest) error {
	ok := false
	for _, r := range Runtimes {
		if m.Runtime == r {
			ok = true
		}
	}
	if !ok {
		return fmt.Errorf("unknown runtime %q (have: %s)", m.Runtime, strings.Join(Runtimes, ", "))
	}
	// The name is joined into filesystem paths (the draft dir and, once adopted,
	// the vendored-tool + skill dirs), so enforce the same identifier rule the
	// adoption boundary uses here too — don't rely on the existence check below
	// to be the only thing standing between a bad name and a bad path.
	if !ValidToolName(m.Name) {
		return fmt.Errorf("tool name %q must be a plain identifier ([A-Za-z0-9._-], starting alphanumeric, no path separators)", m.Name)
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("draft already exists: %s (edit it, or remove it to start over)", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	m.Entrypoint = m.Name

	files := map[string]struct {
		tpl  string
		mode os.FileMode
	}{
		"selftest.sh": {"selftest.sh", 0o755},
		"SKILL.md":    {"SKILL.md", 0o644},
		"README.md":   {"README.md", 0o644},
		// Named "gitignore" in templates/ because go:embed * skips dotfiles;
		// written out as .gitignore so a new tool keeps secrets out of git.
		".gitignore": {"gitignore", 0o644},
	}
	if src := SourceFile(m); src != "" {
		// Compiled/wrapped: source is separate; adoption (`smith use`) produces
		// the entrypoint (Go: compiled binary; python-uv: uv launcher).
		files[src] = struct {
			tpl  string
			mode os.FileMode
		}{"tool." + m.Runtime, 0o644}
	} else {
		// Interpreted: the entrypoint file IS the source and is executable.
		files[m.Entrypoint] = struct {
			tpl  string
			mode os.FileMode
		}{"tool." + m.Runtime, 0o755}
	}
	for name, f := range files {
		content, err := render(f.tpl, m)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), f.mode); err != nil {
			return err
		}
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.yaml"), raw, 0o644)
}

// SaveManifest writes m to dir/manifest.yaml (used by `smith upgrade` when
// reseeding a draft at a bumped version).
func SaveManifest(dir string, m Manifest) error {
	raw, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.yaml"), raw, 0o644)
}

func LoadManifest(dir string) (Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		return Manifest{}, fmt.Errorf("no manifest.yaml in %s (is this a smith draft?): %w", dir, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return Manifest{}, err
	}
	if m.Name == "" || m.Version == "" || m.Entrypoint == "" {
		return Manifest{}, fmt.Errorf("manifest.yaml must set name, version, entrypoint")
	}
	// Name and entrypoint from an untrusted repo are joined into host paths
	// (vendored dir, build target) before the reviewer sees anything — reject
	// traversal/absolute values so a hostile manifest can't escape .smith/.
	if !ValidToolName(m.Name) {
		return Manifest{}, fmt.Errorf("manifest name %q is not a plain identifier ([A-Za-z0-9._-], no path separators)", m.Name)
	}
	if !filepath.IsLocal(m.Entrypoint) {
		return Manifest{}, fmt.Errorf("manifest entrypoint %q must be a relative path inside the repo (no .. or leading /)", m.Entrypoint)
	}
	// Version and runtime are printed into the reviewer's terminal (the review
	// header, the TUI header, the capability summary) before any code is read.
	// A hostile repo could embed terminal escape sequences in them to hide or
	// forge output — the exact thing the review step exists to prevent — so
	// reject any control bytes here, at the parse boundary.
	if hasControlBytes(m.Version) {
		return Manifest{}, fmt.Errorf("manifest version contains control characters")
	}
	if hasControlBytes(m.Runtime) {
		return Manifest{}, fmt.Errorf("manifest runtime contains control characters")
	}
	return m, nil
}

// hasControlBytes reports whether s contains any C0/C1 control character or DEL
// — bytes a terminal may act on as escape/cursor-control sequences.
func hasControlBytes(s string) bool {
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return true
		}
	}
	return false
}
