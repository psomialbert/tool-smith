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
		// Escape: either a CSI sequence (ESC [ ...) or a bare Esc.
		next, _, err := r.ReadRune()
		if err != nil {
			return Key{Kind: KeyEsc}, nil
		}
		if next != '[' && next != 'O' {
			r.UnreadRune()
			return Key{Kind: KeyEsc}, nil
		}
		code, _, err := r.ReadRune()
		if err != nil {
			return Key{Kind: KeyEsc}, nil
		}
		switch code {
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
		case '5', '6':
			// PageUp/PageDown arrive as ESC [ 5 ~ / ESC [ 6 ~.
			if tilde, _, err := r.ReadRune(); err == nil && tilde == '~' {
				if code == '5' {
					return Key{Kind: KeyPageUp}, nil
				}
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
