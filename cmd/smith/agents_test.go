package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// writeToolSkill creates .smith/tools/<name>/SKILL.md with the given description
// under the test's cwd, mirroring what `smith use` vendors.
func writeToolSkill(t *testing.T, name, desc string) {
	t.Helper()
	dir := filepath.Join(smithDir, "tools", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectToolIndexVendoredAndExternal(t *testing.T) {
	t.Chdir(t.TempDir())
	writeToolSkill(t, "calc", "Evaluate an arithmetic expression safely.")

	lock := ProjLock{Tools: map[string]ProjTool{
		"calc": {Source: "https://example.com/calc", Version: "v0.1.1", Runtime: "python", Entrypoint: "calc"},
		"pup":  {Source: "ubi:DataDog/pup", Version: "v0.9.0", Runtime: "mise"},
	}}

	got := projectToolIndex(lock)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	// Deterministic order (sortedToolNames): calc before pup.
	if got[0].Name != "calc" || got[1].Name != "pup" {
		t.Fatalf("order not deterministic: %s, %s", got[0].Name, got[1].Name)
	}
	if got[0].Invoke != "calc --json …" {
		t.Errorf("vendored invoke = %q; want the --json contract", got[0].Invoke)
	}
	if got[0].Trigger != "Evaluate an arithmetic expression safely." {
		t.Errorf("trigger not read from SKILL.md: %q", got[0].Trigger)
	}
	if got[0].External {
		t.Error("vendored tool marked external")
	}
	// External mise CLIs never promised --json, and their source is unreviewed.
	if !got[1].External {
		t.Error("mise tool not marked external")
	}
	if got[1].Invoke != "pup" {
		t.Errorf("external invoke = %q; want bare name", got[1].Invoke)
	}
	if got[1].Trigger != "external CLI via mise (ubi:DataDog/pup) — no source reviewed" {
		t.Errorf("external trigger lost the unreviewed marker: %q", got[1].Trigger)
	}
}

func TestProjectToolIndexMissingSkill(t *testing.T) {
	t.Chdir(t.TempDir())
	lock := ProjLock{Tools: map[string]ProjTool{
		"bare": {Version: "v1.0.0", Runtime: "bash", Entrypoint: "bare"},
	}}
	got := projectToolIndex(lock)
	if len(got) != 1 || got[0].Trigger != "(no trigger described)" {
		t.Fatalf("missing SKILL.md should yield the placeholder trigger, got %+v", got)
	}
}

func TestSanitizeTrigger(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"passthrough", "Evaluate an expression.", "Evaluate an expression."},
		{"newlines collapse", "line one\nline two", "line one line two"},
		{"crlf collapses", "a\r\nb", "a b"},
		{"whitespace runs collapse", "a     b\t\tc", "a b c"},
		{"pipes escaped", "use a|b", `use a\|b`},
		{"trimmed", "  padded  ", "padded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeTrigger(c.in); got != c.want {
				t.Errorf("sanitizeTrigger(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSanitizeTriggerCapsLength(t *testing.T) {
	long := ""
	for i := 0; i < 500; i++ {
		long += "x"
	}
	got := sanitizeTrigger(long)
	if len(got) > 300 {
		t.Errorf("trigger not capped: len = %d, want <= 300", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated trigger should be ellipsized, got tail %q", got[len(got)-1:])
	}
}

// TestSanitizeTriggerCapsLengthUTF8 guards against a raw byte-slice truncation
// landing mid-rune. The real external-tool trigger ("external CLI via mise
// (…) — no source reviewed") already contains multi-byte runes (the ellipsis,
// an em dash), so this isn't hypothetical: a cut that ignores rune boundaries
// can emit invalid UTF-8 while still satisfying the byte-length cap.
func TestSanitizeTriggerCapsLengthUTF8(t *testing.T) {
	long := strings.Repeat("世", 200) // 3 bytes/rune; padded below to shift the
	for pad := 0; pad <= 5; pad++ {  // truncation cut across every byte offset
		in := long + strings.Repeat("x", pad)
		got := sanitizeTrigger(in)
		if !utf8.ValidString(got) {
			t.Errorf("pad=%d: sanitizeTrigger produced invalid UTF-8: %q", pad, got)
		}
		if len(got) > 300 {
			t.Errorf("pad=%d: trigger not capped: len = %d, want <= 300", pad, len(got))
		}
	}
}
