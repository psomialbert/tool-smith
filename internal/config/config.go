// Package config resolves the Toolsmith runtime home ($SMITH_HOME, default
// ~/.toolsmith) and the directory layout inside it. The code repo and the
// runtime home are deliberately separate: the repo holds source and skills,
// the home holds the index, store, provenance log, and sandbox profiles.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"smith/internal/transport"
)

type Paths struct {
	Home            string
	IndexTools      string
	StoreTrusted    string
	StoreQuarantine string
	TrustedBin      string
	ProvenanceLog   string
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
	home = abs
	// Provenance is per-machine: SMITH_HOME syncs via git across machines,
	// and independent per-host chains merge cleanly where one shared
	// append-only file would conflict and break its hash chain.
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return Paths{
		Home:            home,
		IndexTools:      filepath.Join(home, "index", "tools"),
		StoreTrusted:    filepath.Join(home, "store", "trusted"),
		StoreQuarantine: filepath.Join(home, "store", "quarantine"),
		TrustedBin:      filepath.Join(home, "store", "trusted", "bin"),
		ProvenanceLog:   filepath.Join(home, "provenance", host+".jsonl"),
		SandboxProfiles: filepath.Join(home, "sandbox", "profiles"),
	}, nil
}

// FileConfig is $SMITH_HOME/config.yaml.
type FileConfig struct {
	Transport transport.Config `yaml:"transport"`
	// Platforms are mise platform names to capture checksums for when adding
	// external tools (e.g. [macos-arm64, linux-x64, linux-arm64]). Empty =
	// just this machine's platform (mise's default).
	Platforms []string `yaml:"platforms,omitempty"`
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

func (p Paths) EnsureLayout() error {
	dirs := []string{
		p.IndexTools,
		p.StoreTrusted,
		p.StoreQuarantine,
		p.TrustedBin,
		filepath.Dir(p.ProvenanceLog),
		p.SandboxProfiles,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// SMITH_HOME doubles as the index git repo. Binaries (store), the
	// private signing key (keys), and machine-local drafts must never be
	// committed; append any missing ignore lines without clobbering.
	gi := filepath.Join(p.Home, ".gitignore")
	existing, _ := os.ReadFile(gi)
	content := string(existing)
	for _, line := range []string{"store/", "keys/", "drafts/"} {
		if !containsLine(content, line) {
			if content != "" && !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			content += line + "\n"
		}
	}
	if string(existing) != content {
		if err := os.WriteFile(gi, []byte(content), 0o644); err != nil {
			return err
		}
	}
	// Provenance logs are append-only; a union merge concatenates both sides
	// so per-machine <hostname>.jsonl files never conflict on sync. (A same-
	// hostname collision degrades to interleaved lines, which VerifyChain
	// detects — far better than a failed pull.)
	ga := filepath.Join(p.Home, ".gitattributes")
	if _, err := os.Stat(ga); os.IsNotExist(err) {
		if err := os.WriteFile(ga, []byte("provenance/*.jsonl merge=union\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func containsLine(text, line string) bool {
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// Actor identifies who is acting, for provenance. Agent sessions are marked
// as such so a human reviewer can distinguish them at promotion time.
func Actor() string {
	if a := os.Getenv("SMITH_ACTOR"); a != "" {
		return a
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}
	if os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_CODE_SESSION") != "" {
		return "agent:" + user
	}
	return "human:" + user
}
