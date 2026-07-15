// Package reviewtui is smith's interactive source reviewer: a raw-terminal
// UI for reading an authored tool's bundle, selecting line ranges, and
// attaching comments that become an agent-pasteable review report.
//
// The package is split so everything except the raw-terminal setup is pure
// and unit-tested: highlight (this file), keys, model, and render are all
// data-in/data-out; only RunTerminal touches the tty.
package reviewtui

import (
	"path/filepath"
	"strings"
	"unicode"
)

// Theme maps token classes to ANSI SGR parameter strings (the part between
// "\x1b[" and "m"). The "mono" theme leaves everything uncolored, which is
// also what render uses when color is disabled — keeping tests ANSI-free.
type Theme struct {
	Name    string
	Keyword string
	Str     string
	Comment string
	Number  string
	Reset   string
}

var themes = map[string]Theme{
	"dark":  {Name: "dark", Keyword: "38;5;204", Str: "38;5;114", Comment: "38;5;244", Number: "38;5;179", Reset: "0"},
	"light": {Name: "light", Keyword: "38;5;127", Str: "38;5;28", Comment: "38;5;245", Number: "38;5;130", Reset: "0"},
	"mono":  {Name: "mono"}, // no color
}

// ThemeByName returns a known theme, defaulting to "dark". An empty or
// unknown name falls back rather than erroring — a bad config line should
// not break review.
func ThemeByName(name string) Theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return themes["dark"]
}

func (t Theme) colored() bool { return t.Name != "mono" && t.Name != "" }

// LangFor picks a lexer from a filename (and optional first line for a
// shebang). Recognized: python, bash, yaml; everything else is "generic"
// (comment/string/number highlighting, no keyword set).
func LangFor(name, firstLine string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".py":
		return "python"
	case ".sh", ".bash":
		return "bash"
	case ".yaml", ".yml":
		return "yaml"
	}
	if strings.HasPrefix(firstLine, "#!") {
		switch {
		case strings.Contains(firstLine, "python"):
			return "python"
		case strings.Contains(firstLine, "bash"), strings.Contains(firstLine, "sh"):
			return "bash"
		}
	}
	return "generic"
}

var keywords = map[string]map[string]bool{
	"python": setOf("def", "class", "if", "elif", "else", "for", "while", "return",
		"import", "from", "in", "not", "and", "or", "is", "None", "True", "False",
		"try", "except", "finally", "raise", "with", "as", "lambda", "yield", "pass",
		"break", "continue", "global", "nonlocal", "assert", "del"),
	"bash": setOf("if", "then", "else", "elif", "fi", "for", "while", "until", "do",
		"done", "case", "esac", "function", "return", "local", "export", "set",
		"in", "read", "echo", "exit", "shift"),
	"yaml": {},
}

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

func commentChar(lang string) byte {
	// python/bash/yaml/generic all use '#'.
	return '#'
}

// Highlight returns line with ANSI SGR codes applied for lang under theme.
// It is a per-line, stateless scanner: comments (# to EOL when outside a
// string), single/double-quoted strings, numbers, and language keywords.
// Multi-line constructs (triple-quoted strings, heredocs) are highlighted
// per line as ordinary code — acceptable for reading, and it keeps every
// rendered line independently colorable (no ANSI state leaking across lines).
func Highlight(lang, line string, theme Theme) string {
	if !theme.colored() || line == "" {
		return line
	}
	cc := commentChar(lang)
	kw := keywords[lang]
	var b strings.Builder
	runes := []rune(line)
	i := 0
	wrap := func(code, s string) {
		b.WriteString("\x1b[")
		b.WriteString(code)
		b.WriteString("m")
		b.WriteString(s)
		b.WriteString("\x1b[0m")
	}
	for i < len(runes) {
		c := runes[i]
		switch {
		case byte(c) == cc && c < 128:
			wrap(theme.Comment, string(runes[i:]))
			return b.String()
		case c == '\'' || c == '"':
			q := c
			j := i + 1
			for j < len(runes) && runes[j] != q {
				if runes[j] == '\\' && j+1 < len(runes) {
					j++
				}
				j++
			}
			if j < len(runes) {
				j++ // include closing quote
			}
			wrap(theme.Str, string(runes[i:j]))
			i = j
		case unicode.IsDigit(c):
			j := i
			for j < len(runes) && (unicode.IsDigit(runes[j]) || runes[j] == '.' || runes[j] == 'x' || (runes[j] >= 'a' && runes[j] <= 'f')) {
				j++
			}
			wrap(theme.Number, string(runes[i:j]))
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
				j++
			}
			word := string(runes[i:j])
			if kw[word] {
				wrap(theme.Keyword, word)
			} else {
				b.WriteString(word)
			}
			i = j
		default:
			b.WriteRune(c)
			i++
		}
	}
	return b.String()
}
