package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
