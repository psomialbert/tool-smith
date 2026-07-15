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

var Runtimes = []string{"bash", "python", "go"}

// Manifest is the draft manifest (unversioned-draft in spec terms: it exists
// only in the draft dir until registration pins it into the index).
type Manifest struct {
	Name        string       `yaml:"name"`
	Version     string       `yaml:"version"`
	Description string       `yaml:"description"`
	Runtime     string       `yaml:"runtime"`
	Entrypoint  string       `yaml:"entrypoint"`        // file in the draft dir that becomes bin/<name>
	Sandbox     *SandboxSpec `yaml:"sandbox,omitempty"` // per-tool grants beyond the strict default
}

// SandboxSpec is the tool's requested sandbox relaxations. Absent = the
// strict default (no network, writes confined to CWD+TMP). Any grant here is
// surfaced at promotion review — a tool asking for network is a flag to scrutinize.
type SandboxSpec struct {
	Network bool `yaml:"network,omitempty"`
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
	}
	if m.Runtime == "go" {
		// Compiled: the source is main.go; `smith register` builds the
		// entrypoint binary (which is platform-specific).
		files["main.go"] = struct {
			tpl  string
			mode os.FileMode
		}{"tool.go", 0o644}
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
