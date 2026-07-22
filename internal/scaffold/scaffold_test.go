package scaffold

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
