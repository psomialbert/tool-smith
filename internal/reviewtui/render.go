package reviewtui

import (
	"fmt"
	"strconv"
	"strings"
)

// Render produces the full screen as exactly rows lines (each already fit to
// cols). Pure: same model+size+theme always yields the same frame, so the
// layout is unit-tested without a terminal. With a mono theme the output is
// entirely ANSI-free, which keeps assertions simple.
func Render(m *Model, rows, cols int, theme Theme) []string {
	if rows < 3 {
		rows = 3
	}
	if cols < 20 {
		cols = 20
	}
	viewRows := rows - 2
	out := make([]string, 0, rows)

	// Header bar.
	head := fmt.Sprintf(" %s@%s  %s  [%d/%d]", m.Tool, m.Version, m.File().Name, m.cur+1, len(m.Files))
	out = append(out, bar(head, cols, theme))

	// Code area.
	f := m.File()
	numW := len(strconv.Itoa(len(f.Lines)))
	if numW < 2 {
		numW = 2
	}
	lo, hi := m.Sel()
	selecting := m.Anchor() >= 0
	top := m.Top()
	for r := 0; r < viewRows; r++ {
		ln := top + r
		if ln >= len(f.Lines) {
			out = append(out, "")
			continue
		}
		marker := " "
		if selecting && ln >= lo && ln <= hi {
			marker = "*"
		}
		if ln == m.Cursor() {
			marker = ">"
		}
		gutter := fmt.Sprintf("%*d %s ", numW, ln+1, marker)
		avail := cols - len(gutter)
		if avail < 1 {
			avail = 1
		}
		code := truncate(f.Lines[ln], avail)
		if ln == m.Cursor() && theme.colored() {
			out = append(out, gutter+"\x1b[7m"+code+"\x1b[0m")
		} else {
			out = append(out, gutter+Highlight(f.Lang, code, theme))
		}
	}

	// Footer: always mode-appropriate key hints, so the available commands
	// stay visible mid-selection and mid-comment, and the exits are named.
	out = append(out, bar(footer(m), cols, theme))
	return out
}

// footer returns the key-hint line for the current mode.
func footer(m *Model) string {
	switch m.Mode {
	case ModeComment:
		lo, hi := m.Sel()
		rng := strconv.Itoa(lo + 1)
		if hi != lo {
			rng += "-" + strconv.Itoa(hi+1)
		}
		// Cancel/save named first so the way out is always visible even if a
		// long comment truncates the tail.
		return fmt.Sprintf(" COMMENT %s [Enter save · Esc cancel] > %s_", rng, m.Input)
	}
	if m.Selecting() {
		lo, hi := m.Sel()
		return fmt.Sprintf(" SELECT %d-%d · move to extend · c comment · space cancel · q quit", lo+1, hi+1)
	}
	hint := " ↑↓/jk move · space select · c comment · n/p file · g/G ends · q quit"
	if len(m.Comments) > 0 {
		hint += fmt.Sprintf(" · %d comment%s", len(m.Comments), plural(len(m.Comments)))
	}
	return hint
}

// bar fits s to width and, when colored, renders it reverse-video.
func bar(s string, width int, theme Theme) string {
	s = truncate(s, width)
	if len(s) < width {
		s += strings.Repeat(" ", width-len(s))
	}
	if theme.colored() {
		return "\x1b[7m" + s + "\x1b[0m"
	}
	return s
}

// truncate cuts s to at most n runes (width management happens on raw text,
// before any highlighting adds zero-width ANSI).
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
