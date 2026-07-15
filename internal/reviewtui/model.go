package reviewtui

import (
	"fmt"
	"strings"
)

type Mode int

const (
	ModeNormal Mode = iota
	ModeComment
)

type File struct {
	Name  string
	Lang  string
	Lines []string
}

// Comment anchors a note to a 1-based inclusive line range in a file.
type Comment struct {
	File    string
	Start   int
	End     int
	Text    string
	Snippet []string
}

// Model is the full reviewer state. All transitions are methods with no I/O,
// so the loop is a thin driver over them.
type Model struct {
	Tool, Version string
	Files         []File
	Comments      []Comment
	Mode          Mode
	Input         string
	Status        string
	Quit          bool

	cur    int
	cursor []int // per-file line index (0-based)
	top    []int // per-file first visible line
	anchor []int // per-file selection anchor (0-based), -1 = none
}

func New(tool, version string, files []File) *Model {
	m := &Model{Tool: tool, Version: version, Files: files,
		cursor: make([]int, len(files)), top: make([]int, len(files)), anchor: make([]int, len(files))}
	for i := range m.anchor {
		m.anchor[i] = -1
	}
	return m
}

func (m *Model) Cur() int       { return m.cur }
func (m *Model) File() File     { return m.Files[m.cur] }
func (m *Model) Cursor() int    { return m.cursor[m.cur] }
func (m *Model) Top() int       { return m.top[m.cur] }
func (m *Model) Anchor() int    { return m.anchor[m.cur] }
func (m *Model) lineCount() int { return len(m.Files[m.cur].Lines) }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Sel returns the current selection as a 0-based inclusive [lo,hi] range:
// the anchor..cursor span, or just the cursor line when nothing is anchored.
func (m *Model) Sel() (int, int) {
	c := m.cursor[m.cur]
	a := m.anchor[m.cur]
	if a < 0 {
		return c, c
	}
	if a <= c {
		return a, c
	}
	return c, a
}

// scrollTo keeps the cursor within the visible window of viewRows.
func (m *Model) scrollTo(viewRows int) {
	if viewRows < 1 {
		viewRows = 1
	}
	c := m.cursor[m.cur]
	t := m.top[m.cur]
	if c < t {
		t = c
	}
	if c >= t+viewRows {
		t = c - viewRows + 1
	}
	maxTop := m.lineCount() - viewRows
	if maxTop < 0 {
		maxTop = 0
	}
	m.top[m.cur] = clamp(t, 0, maxTop)
}

func (m *Model) Move(delta, viewRows int) {
	m.cursor[m.cur] = clamp(m.cursor[m.cur]+delta, 0, m.lineCount()-1)
	m.scrollTo(viewRows)
}

func (m *Model) Page(pages, viewRows int) { m.Move(pages*viewRows, viewRows) }

func (m *Model) ToStart(viewRows int) { m.cursor[m.cur] = 0; m.scrollTo(viewRows) }
func (m *Model) ToEnd(viewRows int)   { m.cursor[m.cur] = m.lineCount() - 1; m.scrollTo(viewRows) }

func (m *Model) NextFile(viewRows int) {
	m.cur = clamp(m.cur+1, 0, len(m.Files)-1)
	m.scrollTo(viewRows)
}
func (m *Model) PrevFile(viewRows int) {
	m.cur = clamp(m.cur-1, 0, len(m.Files)-1)
	m.scrollTo(viewRows)
}

// ToggleSelect starts a selection anchored at the cursor, or clears it.
func (m *Model) ToggleSelect() {
	if m.anchor[m.cur] < 0 {
		m.anchor[m.cur] = m.cursor[m.cur]
		m.Status = "selecting — move to extend, c to comment"
	} else {
		m.anchor[m.cur] = -1
		m.Status = "selection cleared"
	}
}

func (m *Model) StartComment() {
	m.Mode = ModeComment
	m.Input = ""
}

func (m *Model) CancelComment() {
	m.Mode = ModeNormal
	m.Input = ""
	m.Status = "comment cancelled"
}

func (m *Model) InputRune(r rune) { m.Input += string(r) }

func (m *Model) InputBackspace() {
	if n := len(m.Input); n > 0 {
		m.Input = m.Input[:n-1]
	}
}

// CommitComment records the buffered note against the current selection and
// returns to normal mode. Empty input is a no-op cancel.
func (m *Model) CommitComment() {
	text := strings.TrimSpace(m.Input)
	m.Mode = ModeNormal
	m.Input = ""
	if text == "" {
		m.Status = "empty comment discarded"
		return
	}
	lo, hi := m.Sel()
	f := m.Files[m.cur]
	snippet := append([]string(nil), f.Lines[lo:hi+1]...)
	m.Comments = append(m.Comments, Comment{
		File: f.Name, Start: lo + 1, End: hi + 1, Text: text, Snippet: snippet,
	})
	m.anchor[m.cur] = -1
	m.Status = fmt.Sprintf("comment added (%d total)", len(m.Comments))
}

// Report renders all comments as an agent-pasteable markdown review.
func (m *Model) Report() string {
	if len(m.Comments) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Code review: %s@%s (%d comment%s)\n",
		m.Tool, m.Version, len(m.Comments), plural(len(m.Comments)))
	langByName := map[string]string{}
	for _, f := range m.Files {
		langByName[f.Name] = f.Lang
	}
	for _, c := range m.Comments {
		loc := fmt.Sprintf("%s:%d", c.File, c.Start)
		if c.End != c.Start {
			loc = fmt.Sprintf("%s:%d-%d", c.File, c.Start, c.End)
		}
		fmt.Fprintf(&b, "\n### %s\n", loc)
		lang := langByName[c.File]
		if lang == "generic" {
			lang = ""
		}
		fmt.Fprintf(&b, "```%s\n%s\n```\n", lang, strings.Join(c.Snippet, "\n"))
		fmt.Fprintf(&b, "%s\n", c.Text)
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
