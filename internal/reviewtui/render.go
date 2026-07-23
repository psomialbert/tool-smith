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

	// Defensive: an empty bundle has no current file to render. The CLI guards
	// against this before entering the TUI, but keep the pure renderer safe.
	if len(m.Files) == 0 {
		out = append(out, bar(" (no files to review)", cols, theme))
		for r := 0; r < viewRows; r++ {
			out = append(out, "")
		}
		out = append(out, bar(" q quit", cols, theme))
		return out
	}

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

// bar fits s to width and, when colored, renders it reverse-video. Padding is
// by display width, so a header with wide (CJK/emoji) runes still fills exactly
// one terminal row.
func bar(s string, width int, theme Theme) string {
	s = truncate(s, width)
	if pad := width - dispWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	if theme.colored() {
		return "\x1b[7m" + s + "\x1b[0m"
	}
	return s
}

// dispWidth is the terminal column count of s: wide runes (CJK, Hangul,
// fullwidth forms, common emoji) count as 2, everything else as 1. An
// approximation — good enough to stop wide content from overflowing the row
// and corrupting the frame. Callers sanitize control bytes out beforehand.
func dispWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E,   // CJK radicals, Kangxi
		r >= 0x3041 && r <= 0x33FF,   // Hiragana..CJK compat
		r >= 0x3400 && r <= 0x4DBF,   // CJK ext A
		r >= 0x4E00 && r <= 0x9FFF,   // CJK unified
		r >= 0xA000 && r <= 0xA4CF,   // Yi
		r >= 0xAC00 && r <= 0xD7A3,   // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF,   // CJK compat ideographs
		r >= 0xFE30 && r <= 0xFE4F,   // CJK compat forms
		r >= 0xFF00 && r <= 0xFF60,   // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,   // fullwidth signs
		r >= 0x1F300 && r <= 0x1FAFF, // emoji & pictographs
		r >= 0x20000 && r <= 0x3FFFD: // CJK ext B+
		return 2
	}
	return 1
}

// truncate cuts s to at most n display columns, appending an ellipsis when it
// had to cut (width management happens on raw text, before any highlighting
// adds zero-width ANSI).
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if dispWidth(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	// Reserve one column for the ellipsis; stop before exceeding n-1.
	w := 0
	var b strings.Builder
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > n-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}
