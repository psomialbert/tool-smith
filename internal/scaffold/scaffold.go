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
	"strings"

	"gopkg.in/yaml.v3"
)

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
	}
	if src := SourceFile(m); src != "" {
		// Compiled/wrapped: source is separate; `smith register` produces the
		// entrypoint (Go: compiled binary; python-uv: uv launcher).
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
	return m, nil
}
