// Package index reads and writes the local manifest: one YAML file per
// tool@version under index/tools/<name>/<version>.yaml. Index content is
// data, never instructions (§5.3). Signing arrives in Phase 2.
package index

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// PlatformAny marks platform-independent artifacts (bash/python scripts).
const PlatformAny = "any"

// CurrentPlatform is the platform key for binaries built/installed here.
func CurrentPlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

type Artifact struct {
	Path   string `yaml:"path" json:"path"` // relative to the tool's store dir
	Sha256 string `yaml:"sha256" json:"sha256"`
}

// Blob points at the pushed archive of an authored tool on the shared
// transport — what machine B fetches (§5.3 source rule).
type Blob struct {
	Locator string `yaml:"locator" json:"locator"` // e.g. github-release:owner/repo#tag#asset | local-dir:file.tgz
	Sha256  string `yaml:"sha256" json:"sha256"`
}

// SchemaVersion is the current index-entry format version. Bump it when the
// on-disk shape changes and add a migration in migrate(); Load tolerates
// older entries (0 == pre-versioning) so a stale checkout still verifies.
const SchemaVersion = 1

type Entry struct {
	Schema    int        `yaml:"schema" json:"schema"`
	Name      string     `yaml:"name" json:"name"`
	Version   string     `yaml:"version" json:"version"`
	Platform  string     `yaml:"platform" json:"platform"` // "any" for scripts, else GOOS-GOARCH
	Tier      int        `yaml:"tier" json:"tier"`         // 0 authored | 1 trusted | 2 external quarantine
	Origin    string     `yaml:"origin" json:"origin"`
	Source    string     `yaml:"source" json:"source"` // fetchable spec; machine-local only valid below Tier 1
	AddedAt   string     `yaml:"added_at" json:"added_at"`
	Artifacts []Artifact `yaml:"artifacts" json:"artifacts"`           // executables (symlinked on promote)
	Docs      []Artifact `yaml:"docs,omitempty" json:"docs,omitempty"` // pinned non-executables (SKILL.md, selftest, manifest)
	Blob      *Blob      `yaml:"blob,omitempty" json:"blob,omitempty"` // authored tools: shared-transport archive
	// Verification is the strongest check the source passed at add time:
	// gh-attestation | aqua-registry | mise-checksum | tofu (see internal/attest).
	Verification string `yaml:"verification,omitempty" json:"verification,omitempty"`
	// Locked marks an external tool whose cross-machine integrity anchor is
	// the committed mise.lock (archive checksums), not per-platform index
	// pins. A Locked entry with no Artifacts is a stub: the tool is approved
	// for this platform but not yet materialized here — `smith fetch` installs
	// it via the lockfile and records local pins in the store.
	Locked bool `yaml:"locked,omitempty" json:"locked,omitempty"`
}

func entryPath(indexTools, name, version, platform string) string {
	return filepath.Join(indexTools, name, version, platform+".yaml")
}

func Save(indexTools string, e Entry) error {
	if e.Platform == "" {
		return fmt.Errorf("index entry %s@%s has no platform", e.Name, e.Version)
	}
	e.Schema = SchemaVersion
	if err := os.MkdirAll(filepath.Join(indexTools, e.Name, e.Version), 0o755); err != nil {
		return err
	}
	raw, err := yaml.Marshal(e)
	if err != nil {
		return err
	}
	return os.WriteFile(entryPath(indexTools, e.Name, e.Version, e.Platform), raw, 0o644)
}

// Load returns the entry for this machine's platform, falling back to the
// platform-independent ("any") entry.
func Load(indexTools, name, version string) (Entry, error) {
	var lastErr error
	for _, plat := range []string{CurrentPlatform(), PlatformAny} {
		raw, err := os.ReadFile(entryPath(indexTools, name, version, plat))
		if err != nil {
			lastErr = err
			continue
		}
		var e Entry
		if err := yaml.Unmarshal(raw, &e); err != nil {
			return Entry{}, fmt.Errorf("parsing index entry %s@%s (%s): %w", name, version, plat, err)
		}
		return migrate(e), nil
	}
	return Entry{}, fmt.Errorf("no %s or %s entry for %s@%s: %w",
		CurrentPlatform(), PlatformAny, name, version, lastErr)
}

// List returns entries usable on this machine (current platform or "any").
// ListAll returns every entry for every platform (verify, publish).
func List(indexTools string) ([]Entry, error) {
	return list(indexTools, false)
}

func ListAll(indexTools string) ([]Entry, error) {
	return list(indexTools, true)
}

func list(indexTools string, all bool) ([]Entry, error) {
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
		for _, vd := range versions {
			if !vd.IsDir() {
				continue
			}
			plats, err := os.ReadDir(filepath.Join(indexTools, nd.Name(), vd.Name()))
			if err != nil {
				return nil, err
			}
			for _, pf := range plats {
				plat := strings.TrimSuffix(pf.Name(), ".yaml")
				if pf.IsDir() || plat == pf.Name() {
					continue
				}
				if !all && plat != CurrentPlatform() && plat != PlatformAny {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(indexTools, nd.Name(), vd.Name(), pf.Name()))
				if err != nil {
					return nil, err
				}
				var e Entry
				if err := yaml.Unmarshal(raw, &e); err != nil {
					return nil, fmt.Errorf("parsing %s: %w", pf.Name(), err)
				}
				out = append(out, migrate(e))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Version != out[j].Version {
			return versionLess(out[i].Version, out[j].Version)
		}
		return out[i].Platform < out[j].Platform
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

// migrate upgrades an entry read from disk to the current schema in memory.
// Entries written before schema versioning carry Schema==0; the platform
// backfill mirrors what pre-versioning Load assumed. Save always stamps the
// current SchemaVersion, so rewriting an entry persists the migration.
func migrate(e Entry) Entry {
	if e.Schema == 0 {
		if e.Platform == "" {
			e.Platform = PlatformAny
		}
	}
	e.Schema = SchemaVersion
	return e
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
