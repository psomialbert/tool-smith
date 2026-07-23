package scaffold

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadManifestRejectsTraversal guards the adoption-path fix: a hostile tool
// repo must not be able to feed a "../"-laden name/entrypoint that escapes
// .smith/ when joined into a filesystem path (arbitrary delete/write before the
// human ever reviews the vendored diff).
func TestLoadManifestRejectsTraversal(t *testing.T) {
	cases := []struct {
		name, manifest string
	}{
		{"name-traversal", "name: ../../../../tmp/pwn\nversion: 0.1.0\nentrypoint: pwn\nruntime: bash\n"},
		{"name-slash", "name: evil/sub\nversion: 0.1.0\nentrypoint: x\nruntime: bash\n"},
		{"name-dotdot", "name: ..\nversion: 0.1.0\nentrypoint: x\nruntime: bash\n"},
		{"entrypoint-traversal", "name: ok\nversion: 0.1.0\nentrypoint: ../../../home/victim/.local/bin/x\nruntime: go\n"},
		{"entrypoint-absolute", "name: ok\nversion: 0.1.0\nentrypoint: /etc/cron.d/x\nruntime: bash\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(c.manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadManifest(dir); err == nil {
				t.Fatalf("LoadManifest accepted a traversal manifest: %s", c.manifest)
			}
		})
	}
	// A legitimate manifest (including a subdir entrypoint) still loads.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "manifest.yaml"),
		[]byte("name: greet\nversion: 0.1.0\nentrypoint: bin/greet\nruntime: bash\n"), 0o644)
	if _, err := LoadManifest(dir); err != nil {
		t.Fatalf("LoadManifest rejected a valid manifest: %v", err)
	}
}

// TestManifestOmitsDescriptionSkillKeepsIt locks in the single-source-of-truth
// for a tool's description: it lives in the SKILL.md frontmatter (the agent
// trigger), NOT in manifest.yaml. Two descriptions drift — an agent updated the
// (vestigial) manifest one and left the live SKILL trigger stale.
func TestManifestOmitsDescriptionSkillKeepsIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "greet")
	desc := "print a greeting to stdout"
	if err := Create(dir, Manifest{
		Name: "greet", Version: "0.1.0", Description: desc, Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "description") || strings.Contains(string(manifest), desc) {
		t.Fatalf("manifest.yaml must not carry a description (single source is SKILL.md):\n%s", manifest)
	}
	skill, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(skill), desc) {
		t.Fatalf("SKILL.md must carry the description:\n%s", skill)
	}
	// LoadManifest still works without a serialized description.
	if _, err := LoadManifest(dir); err != nil {
		t.Fatalf("LoadManifest failed after dropping description: %v", err)
	}
}

// TestTemplatesHaveNoRetiredV1Verbs guards against the scaffold drifting back
// to the retired personal-index model. Every file a `smith new` stamps out
// must describe the v2 flow (new → use → install), never register/run/promote,
// Tier 0/1, or quarantine. This drift shipped once and misled an agent into
// building a tool the v1 way — keep the templates honest.
func TestTemplatesHaveNoRetiredV1Verbs(t *testing.T) {
	retired := []string{
		"smith register", "smith run ", "smith promote",
		"Tier 0", "Tier 1", "Tier 2", "quarantine", "registration",
	}
	err := fs.WalkDir(templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := templates.ReadFile(path)
		if err != nil {
			return err
		}
		body := string(raw)
		for _, term := range retired {
			if strings.Contains(body, term) {
				t.Errorf("%s contains retired v1 term %q", path, term)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestToolSkillTemplateDescribesToolNotSmith guards the separation of concerns:
// a tool's SKILL.md must describe the TOOL (what it does, its contract, when to
// use it) and say nothing about how smith works. smith mechanics change between
// versions; baked into a tool skill they go stale and get duplicated into every
// project that vendors the tool. Only smith + cli-creator describe smith.
func TestToolSkillTemplateDescribesToolNotSmith(t *testing.T) {
	raw, err := templates.ReadFile("templates/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(raw))
	for _, term := range []string{
		"smith list", "smith run", "smith install", "smith use", "smith promote",
		"tier", "sandbox", "adopt", "vendored", "quarantine", "on path",
	} {
		if strings.Contains(body, term) {
			t.Errorf("tool SKILL.md template leaks smith-mechanics term %q — describe the tool, not smith", term)
		}
	}
}

func TestCreateScaffoldsBundleWithReadme(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "greet")
	if err := Create(dir, Manifest{
		Name: "greet", Version: "0.1.0", Description: "print a greeting", Runtime: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"greet", "selftest.sh", "SKILL.md", "manifest.yaml", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("scaffold missing %s: %v", f, err)
		}
	}
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(readme)
	if !strings.Contains(s, "# greet") || !strings.Contains(s, "print a greeting") ||
		!strings.Contains(s, "smith use") {
		t.Fatalf("README missing rendered name/desc/adopt instructions:\n%s", s)
	}
}
