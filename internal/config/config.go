// Package config resolves smith's small runtime home ($SMITH_HOME, default
// ~/.toolsmith) — used only for generated sandbox profiles now — and the
// user's config.yaml (review + skills settings). Tools themselves live in
// git repos and are adopted per project under .smith/ (see cmd/smith/adopt.go).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Paths struct {
	Home            string
	SandboxProfiles string
}

func Resolve() (Paths, error) {
	home := os.Getenv("SMITH_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolving home dir: %w", err)
		}
		home = filepath.Join(userHome, ".toolsmith")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Home: abs, SandboxProfiles: filepath.Join(abs, "sandbox", "profiles")}, nil
}

func (p Paths) EnsureLayout() error {
	return os.MkdirAll(p.SandboxProfiles, 0o755)
}

// FileConfig is $SMITH_HOME/config.yaml.
type FileConfig struct {
	// Editor is a persistent fallback for `smith review --edit` when neither
	// $VISUAL nor $EDITOR is exported (e.g. "nano", "code --wait").
	Editor string `yaml:"editor,omitempty"`
	// Review configures the interactive reviewer.
	Review ReviewConfig `yaml:"review,omitempty"`
	// Skills configures where adopted tools' SKILL.md files are exported.
	Skills SkillsConfig `yaml:"skills,omitempty"`
	// Allowlist is an optional org policy: only sources matching it may be
	// adopted with `smith use`. Lives in $SMITH_HOME/config.yaml (provisioned
	// per machine, not editable in a project PR). Absent = adopt anything.
	Allowlist AllowlistConfig `yaml:"allowlist,omitempty"`
}

type AllowlistConfig struct {
	Repos []string `yaml:"repos,omitempty"` // glob patterns, e.g. "github.com/acme/*"
	File  string   `yaml:"file,omitempty"`  // path to a file of patterns (one per line)
}

type SkillsConfig struct {
	Dir    string `yaml:"dir,omitempty"`    // default ~/.claude/skills
	Export string `yaml:"export,omitempty"` // always (default) | never
}

// SkillsDir resolves the skills directory: $SMITH_SKILLS_DIR (also honored by
// scripts/install.sh) > config skills.dir > ~/.claude/skills. Expands a
// leading ~.
func (c FileConfig) SkillsDir() string {
	d := os.Getenv("SMITH_SKILLS_DIR")
	if d == "" {
		d = c.Skills.Dir
	}
	if d == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".claude", "skills")
	}
	if strings.HasPrefix(d, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, d[2:])
	}
	return d
}

type ReviewConfig struct {
	Theme   string `yaml:"theme,omitempty"`   // dark | light | mono
	Color   string `yaml:"color,omitempty"`   // auto | always | never
	Default string `yaml:"default,omitempty"` // tui (default) | plain | edit | paths
}

func (p Paths) LoadFileConfig() (FileConfig, error) {
	var c FileConfig
	raw, err := os.ReadFile(filepath.Join(p.Home, "config.yaml"))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, yaml.Unmarshal(raw, &c)
}
