// Package index reads and writes the local manifest: one YAML file per
// tool@version under index/tools/<name>/<version>.yaml. Index content is
// data, never instructions (§5.3). Signing arrives in Phase 2.
package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Artifact struct {
	Path   string `yaml:"path" json:"path"` // relative to the tool's store dir
	Sha256 string `yaml:"sha256" json:"sha256"`
}

type Entry struct {
	Name      string     `yaml:"name" json:"name"`
	Version   string     `yaml:"version" json:"version"`
	Tier      int        `yaml:"tier" json:"tier"` // 0 authored | 1 trusted | 2 external quarantine
	Origin    string     `yaml:"origin" json:"origin"`
	Source    string     `yaml:"source" json:"source"` // fetchable spec; machine-local only valid below Tier 1
	AddedAt   string     `yaml:"added_at" json:"added_at"`
	Artifacts []Artifact `yaml:"artifacts" json:"artifacts"` // executables (symlinked on promote)
	Docs      []Artifact `yaml:"docs,omitempty" json:"docs,omitempty"` // pinned non-executables (SKILL.md, selftest, manifest)
}

func entryPath(indexTools, name, version string) string {
	return filepath.Join(indexTools, name, version+".yaml")
}

func Save(indexTools string, e Entry) error {
	if err := os.MkdirAll(filepath.Join(indexTools, e.Name), 0o755); err != nil {
		return err
	}
	raw, err := yaml.Marshal(e)
	if err != nil {
		return err
	}
	return os.WriteFile(entryPath(indexTools, e.Name, e.Version), raw, 0o644)
}

func Load(indexTools, name, version string) (Entry, error) {
	raw, err := os.ReadFile(entryPath(indexTools, name, version))
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	if err := yaml.Unmarshal(raw, &e); err != nil {
		return Entry{}, fmt.Errorf("parsing index entry %s@%s: %w", name, version, err)
	}
	return e, nil
}

func List(indexTools string) ([]Entry, error) {
	var out []Entry
	names, err := os.ReadDir(indexTools)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, nd := range names {
		if !nd.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(indexTools, nd.Name()))
		if err != nil {
			return nil, err
		}
		for _, vf := range versions {
			if vf.IsDir() || !strings.HasSuffix(vf.Name(), ".yaml") {
				continue
			}
			e, err := Load(indexTools, nd.Name(), strings.TrimSuffix(vf.Name(), ".yaml"))
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return versionLess(out[i].Version, out[j].Version)
	})
	return out, nil
}

// Resolve finds an entry by name, and by version when given; with no version
// it picks the single installed version, or the highest when several exist.
func Resolve(indexTools, name, version string) (Entry, error) {
	if version != "" {
		return Load(indexTools, name, version)
	}
	all, err := List(indexTools)
	if err != nil {
		return Entry{}, err
	}
	var candidates []Entry
	for _, e := range all {
		if e.Name == name {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return Entry{}, fmt.Errorf("tool %q not in index", name)
	}
	return candidates[len(candidates)-1], nil
}

// versionLess is a best-effort dotted-numeric compare with string fallback.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, aerr := strconv.Atoi(as[i])
		bi, berr := strconv.Atoi(bs[i])
		if aerr != nil || berr != nil {
			if as[i] != bs[i] {
				return as[i] < bs[i]
			}
			continue
		}
		if ai != bi {
			return ai < bi
		}
	}
	return len(as) < len(bs)
}
