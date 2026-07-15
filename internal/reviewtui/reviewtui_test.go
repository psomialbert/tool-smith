package reviewtui

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func sampleFiles() []File {
	return []File{
		{Name: "bin/calc", Lang: "python", Lines: []string{
			"import ast", "def f(x):", "    return x + 1", "# trailing comment"}},
		{Name: "SKILL.md", Lang: "generic", Lines: []string{"# calc", "safe evaluator"}},
	}
}

func TestParseKey(t *testing.T) {
	cases := []struct {
		in   string
		want KeyKind
	}{
		{"\x1b[A", KeyUp}, {"\x1b[B", KeyDown}, {"\x1b[C", KeyRight}, {"\x1b[D", KeyLeft},
		{"\x1b[5~", KeyPageUp}, {"\x1b[6~", KeyPageDown}, {"\x1b[H", KeyHome}, {"\x1b[F", KeyEnd},
		{"\r", KeyEnter}, {"\x7f", KeyBackspace}, {"\x1b", KeyEsc}, {"\x03", KeyCtrlC},
	}
	for _, c := range cases {
		k, err := ParseKey(bufio.NewReader(strings.NewReader(c.in)))
		if err != nil || k.Kind != c.want {
			t.Errorf("ParseKey(%q) = %v (err %v), want kind %v", c.in, k.Kind, err, c.want)
		}
	}
	k, _ := ParseKey(bufio.NewReader(strings.NewReader("x")))
	if k.Kind != KeyRune || k.Rune != 'x' {
		t.Errorf("rune parse got %v/%q", k.Kind, k.Rune)
	}
}

func TestHighlight(t *testing.T) {
	dark := ThemeByName("dark")
	got := Highlight("python", "def f(): # note", dark)
	if !strings.Contains(got, "\x1b[") {
		t.Fatal("expected ANSI codes in highlighted output")
	}
	// mono theme must not colorize (keeps other tests ANSI-free).
	if mono := Highlight("python", "def f()", ThemeByName("mono")); strings.Contains(mono, "\x1b[") {
		t.Fatalf("mono theme colorized: %q", mono)
	}
	if LangFor("x.py", "") != "python" || LangFor("s", "#!/usr/bin/env bash") != "bash" ||
		LangFor("c.yaml", "") != "yaml" || LangFor("READ.md", "") != "generic" {
		t.Fatal("LangFor misdetected")
	}
}

func TestModelSelectionAndReport(t *testing.T) {
	m := New("calc", "0.1.0", sampleFiles())
	// select lines 2-3 on file 0, comment.
	m.Move(1, 10) // cursor -> line 2 (index 1)
	m.ToggleSelect()
	m.Move(1, 10) // extend to index 2
	lo, hi := m.Sel()
	if lo != 1 || hi != 2 {
		t.Fatalf("Sel = %d-%d, want 1-2", lo, hi)
	}
	m.StartComment()
	for _, r := range "check the +1" {
		m.InputRune(r)
	}
	m.CommitComment()
	if len(m.Comments) != 1 {
		t.Fatalf("comments = %d", len(m.Comments))
	}
	c := m.Comments[0]
	if c.File != "bin/calc" || c.Start != 2 || c.End != 3 || c.Text != "check the +1" {
		t.Fatalf("bad comment: %+v", c)
	}
	rep := m.Report()
	for _, want := range []string{"calc@0.1.0 (1 comment)", "bin/calc:2-3", "```python", "return x + 1", "check the +1"} {
		if !strings.Contains(rep, want) {
			t.Fatalf("report missing %q:\n%s", want, rep)
		}
	}
}

func TestRenderCursorAndSelectionMarkers(t *testing.T) {
	m := New("calc", "0.1.0", sampleFiles())
	m.ToggleSelect()
	m.Move(1, 10)
	frame := Render(m, 8, 60, ThemeByName("mono"))
	joined := strings.Join(frame, "\n")
	if !strings.Contains(joined, "calc@0.1.0") || !strings.Contains(joined, "[1/2]") {
		t.Fatal("header missing tool/file index")
	}
	if !strings.Contains(joined, "1 *") || !strings.Contains(joined, "2 >") {
		t.Fatalf("expected selection '*' on line 1 and cursor '>' on line 2:\n%s", joined)
	}
}

func TestFooterAlwaysShowsModeHints(t *testing.T) {
	m := New("calc", "0.1.0", sampleFiles())
	if !strings.Contains(footer(m), "space select") || !strings.Contains(footer(m), "q quit") {
		t.Fatalf("normal footer missing hints: %q", footer(m))
	}
	m.ToggleSelect()
	sel := footer(m)
	if !strings.Contains(sel, "SELECT") || !strings.Contains(sel, "space cancel") || !strings.Contains(sel, "c comment") {
		t.Fatalf("select footer must show cancel + comment hints: %q", sel)
	}
	m.StartComment()
	m.InputRune('h')
	cm := footer(m)
	if !strings.Contains(cm, "Esc cancel") || !strings.Contains(cm, "Enter save") || !strings.Contains(cm, "h_") {
		t.Fatalf("comment footer must show save/cancel + input: %q", cm)
	}
}

// End-to-end through the real loop with scripted keys — proves ParseKey,
// dispatch, model, and quit all compose. Keys: down, space, down (select
// 2-3), c, type text, Enter, q.
func TestRunScripted(t *testing.T) {
	m := New("calc", "0.1.0", sampleFiles())
	// down, select, down (range 2-3), c, type, Enter, q
	keys := "\x1b[B" + " " + "\x1b[B" + "c" + "needs a kwargs check" + "\r" + "q"
	var out bytes.Buffer
	if err := Run(m, strings.NewReader(keys), &out, 10, 70, ThemeByName("mono")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !m.Quit {
		t.Fatal("loop did not quit")
	}
	if len(m.Comments) != 1 || m.Comments[0].Start != 2 || m.Comments[0].End != 3 {
		t.Fatalf("scripted run comment wrong: %+v", m.Comments)
	}
	if !strings.Contains(m.Report(), "needs a kwargs check") {
		t.Fatal("report missing scripted comment text")
	}
	if out.Len() == 0 {
		t.Fatal("no frames written")
	}
}

func TestRunEOFStops(t *testing.T) {
	m := New("calc", "0.1.0", sampleFiles())
	var out bytes.Buffer
	if err := Run(m, strings.NewReader(""), &out, 10, 70, ThemeByName("mono")); err != nil {
		t.Fatalf("empty input should stop cleanly, got %v", err)
	}
}
