package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorruptConfigFailsClosed guards the allowlist fail-open bug: a
// config.yaml that won't parse must block adoption, not silently disable the
// allowlist. The error is returned before any clone, so no network is needed.
func TestCorruptConfigFailsClosed(t *testing.T) {
	p := testPaths(t)
	if err := os.WriteFile(filepath.Join(p.Home, "config.yaml"), []byte("allowlist: [oops\n  not: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	restore, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(restore)

	err := cmdUse(p, []string{"acme/whatever"})
	if err == nil {
		t.Fatal("cmdUse accepted a corrupt config (allowlist failed open)")
	}
	if !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("error should point at config.yaml, got: %v", err)
	}
}

// TestExpandSourceHostQualified guards the allowlist-bypass fix: a
// "host.tld/owner/repo" source must canonicalize to an https URL so `git clone`
// can't be tricked into cloning a local relative directory of that name (which
// a host-based allowlist pattern would wrongly permit).
func TestExpandSourceHostQualified(t *testing.T) {
	cases := map[string]string{
		"github.com/acme/evil":     "https://github.com/acme/evil",
		"gitlab.com/x/y":           "https://gitlab.com/x/y",
		"example.com/a/b/c":        "https://example.com/a/b/c",
		"foo/bar/baz":              "foo/bar/baz", // no dotted host segment: unchanged
		"github.com/acme/evil.git": "https://github.com/acme/evil.git",
	}
	for in, want := range cases {
		if got := expandSource(in); got != want {
			t.Errorf("expandSource(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestValidCloneURL guards against git remote-helper RCE and option injection
// through an attacker-influenced clone source.
func TestValidCloneURL(t *testing.T) {
	bad := []string{
		`ext::sh -c "touch /tmp/pwned"`,
		"fd::17",
		"-oProxyCommand=evil",
		"--upload-pack=evil",
	}
	for _, s := range bad {
		if err := validCloneURL(s); err == nil {
			t.Errorf("validCloneURL(%q) = nil, want error", s)
		}
	}
	good := []string{
		"https://github.com/acme/x",
		"git@github.com:acme/x.git",
		"/abs/path/repo",
		"./rel/repo",
		"https://example.com/a::b", // "::" after a slash is a path, not a transport
	}
	for _, s := range good {
		if err := validCloneURL(s); err != nil {
			t.Errorf("validCloneURL(%q) = %v, want nil", s, err)
		}
	}
}

// TestUnexpectedFiles guards the injection where a file dropped next to a
// vendored entrypoint isn't covered by the bundle hash yet can hijack
// execution (e.g. a shadowing python module).
func TestUnexpectedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"tool.py", "manifest.yaml", "selftest.sh", "SKILL.md"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	expected := []string{"tool.py", "manifest.yaml", "selftest.sh", "SKILL.md"}
	if extra := unexpectedFiles(dir, expected); len(extra) != 0 {
		t.Fatalf("clean dir flagged extras: %v", extra)
	}
	// A shadowing module dropped next to the entrypoint must be detected.
	os.WriteFile(filepath.Join(dir, "requests.py"), []byte("import os"), 0o644)
	os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "pkg", "sneaky.py"), []byte("x"), 0o644)
	extra := unexpectedFiles(dir, expected)
	if len(extra) != 2 || extra[0] != "pkg/sneaky.py" || extra[1] != "requests.py" {
		t.Fatalf("expected [pkg/sneaky.py requests.py], got %v", extra)
	}
}

// TestShortSha guards against the install-time panic on a short/empty pin.
func TestShortSha(t *testing.T) {
	if got := shortSha(""); got != "(none)" {
		t.Errorf("shortSha(\"\") = %q", got)
	}
	if got := shortSha("abc"); got != "abc" {
		t.Errorf("shortSha short = %q", got)
	}
	if got := shortSha("0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("shortSha long = %q", got)
	}
}

// TestSanitizeForDisplay guards the review-UI escape-injection fix: terminal
// control sequences in untrusted source must be rendered visible, never
// executed.
func TestSanitizeForDisplay(t *testing.T) {
	// ESC-based conceal/erase becomes visible caret notation.
	if got := sanitizeForDisplay("x\x1b[8m"); got != "x^[[8m" {
		t.Errorf("ESC not neutralized: %q", got)
	}
	// A bare carriage return can't reposition the cursor.
	if got := sanitizeForDisplay("real\rfake"); got != "real^Mfake" {
		t.Errorf("CR not neutralized: %q", got)
	}
	// Tabs expand to spaces (no width miscount, no raw control byte).
	if got := sanitizeForDisplay("\tx"); got != "    x" {
		t.Errorf("tab not expanded: %q", got)
	}
	// Ordinary (including wide) text is untouched.
	if got := sanitizeForDisplay("héllo 世界"); got != "héllo 世界" {
		t.Errorf("plain text altered: %q", got)
	}
}
