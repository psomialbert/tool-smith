package attest

import "testing"

func TestBackendLevel(t *testing.T) {
	cases := map[string]string{
		"aqua:cli/cli":               LevelRegistry,
		"aqua:jqlang/jq":             LevelRegistry,
		"ubi:BurntSushi/ripgrep":     LevelTOFU,
		"asdf:mise-plugins/mise-eza": LevelTOFU,
		"cargo:magika-cli":           LevelTOFU,
		"go:github.com/onsi/ginkgo":  LevelTOFU,
		"":                           LevelTOFU,
	}
	for spec, want := range cases {
		if got := BackendLevel(spec); got != want {
			t.Errorf("BackendLevel(%q) = %q, want %q", spec, got, want)
		}
	}
}

func TestGitHubRepo(t *testing.T) {
	cases := map[string]string{
		"aqua:cli/cli":           "cli/cli",
		"ubi:BurntSushi/ripgrep": "BurntSushi/ripgrep",
		"cargo:magika-cli":       "",
		"go:github.com/x/y/z":    "",
		"aqua:deep/nested/path":  "",
		"":                       "",
	}
	for spec, want := range cases {
		if got := GitHubRepo(spec); got != want {
			t.Errorf("GitHubRepo(%q) = %q, want %q", spec, got, want)
		}
	}
}
