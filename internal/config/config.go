// Package config resolves the Toolsmith runtime home ($SMITH_HOME, default
// ~/.toolsmith) and the directory layout inside it. The code repo and the
// runtime home are deliberately separate: the repo holds source and skills,
// the home holds the index, store, provenance log, and sandbox profiles.
package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	return Paths{
		Home:            home,
		IndexTools:      filepath.Join(home, "index", "tools"),
		StoreTrusted:    filepath.Join(home, "store", "trusted"),
		StoreQuarantine: filepath.Join(home, "store", "quarantine"),
		TrustedBin:      filepath.Join(home, "store", "trusted", "bin"),
		ProvenanceLog:   filepath.Join(home, "provenance", "log.jsonl"),
		SandboxProfiles: filepath.Join(home, "sandbox", "profiles"),
	}, nil
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
	// The store holds binaries; it must never be committed to the index repo.
	gi := filepath.Join(p.Home, ".gitignore")
	if _, err := os.Stat(gi); os.IsNotExist(err) {
		if err := os.WriteFile(gi, []byte("store/\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
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
