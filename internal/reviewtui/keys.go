package reviewtui

import (
	"bufio"
	"io"
)

type KeyKind int

const (
	KeyRune KeyKind = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyPageUp
	KeyPageDown
	KeyHome
	KeyEnd
	KeyEnter
	KeyBackspace
	KeyEsc
	KeyCtrlC
	KeyUnknown
)

type Key struct {
	Kind KeyKind
	Rune rune
}

// ParseKey reads one key event from r, decoding the ANSI escape sequences a
// terminal sends for arrows/page/home/end. It is the pure counterpart to the
// raw-terminal loop: feed it recorded bytes in tests, real stdin in RunTerminal.
func ParseKey(r *bufio.Reader) (Key, error) {
	c, _, err := r.ReadRune()
	if err != nil {
		return Key{Kind: KeyUnknown}, err
	}
	switch c {
	case '\r', '\n':
		return Key{Kind: KeyEnter}, nil
	case 127, 8:
		return Key{Kind: KeyBackspace}, nil
	case 3:
		return Key{Kind: KeyCtrlC}, nil
	case 0x1b:
		// Escape: either a CSI/SS3 sequence (ESC [ ... / ESC O ...) or a bare Esc.
		next, _, err := r.ReadRune()
		if err != nil {
			return Key{Kind: KeyEsc}, nil
		}
		if next != '[' && next != 'O' {
			r.UnreadRune()
			return Key{Kind: KeyEsc}, nil
		}
		// Consume the WHOLE sequence: parameter/intermediate bytes (digits, ';',
		// etc.) up to and including the final byte (0x40–0x7E). This keeps a
		// modified key like ESC [ 1 ; 5 A (Ctrl-Up) from leaving "1;5A" in the
		// stream to be misread as printable runes typed into a comment.
		var params []rune
		var final rune
		for {
			c, _, err := r.ReadRune()
			if err != nil {
				return Key{Kind: KeyEsc}, nil
			}
			if c >= 0x40 && c <= 0x7e {
				final = c
				break
			}
			params = append(params, c)
		}
		switch final {
		case 'A':
			return Key{Kind: KeyUp}, nil
		case 'B':
			return Key{Kind: KeyDown}, nil
		case 'C':
			return Key{Kind: KeyRight}, nil
		case 'D':
			return Key{Kind: KeyLeft}, nil
		case 'H':
			return Key{Kind: KeyHome}, nil
		case 'F':
			return Key{Kind: KeyEnd}, nil
		case '~':
			// PageUp/PageDown arrive as ESC [ 5 ~ / ESC [ 6 ~.
			switch string(params) {
			case "5":
				return Key{Kind: KeyPageUp}, nil
			case "6":
				return Key{Kind: KeyPageDown}, nil
			}
			return Key{Kind: KeyUnknown}, nil
		default:
			return Key{Kind: KeyUnknown}, nil
		}
	default:
		if c < 32 {
			return Key{Kind: KeyUnknown}, nil
		}
		return Key{Kind: KeyRune, Rune: c}, nil
	}
}

// keyReader wraps any io.Reader for the loop.
func keyReader(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }
