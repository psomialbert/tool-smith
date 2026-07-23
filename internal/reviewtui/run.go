package reviewtui

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// Run is the event loop over a Model: read a key, apply a transition, redraw.
// It reads from in and writes frames to out, so tests drive it with recorded
// bytes and a buffer — no terminal required. It stops when the model sets
// Quit (q / Ctrl-C). rows/cols are the viewport dimensions.
func Run(m *Model, in io.Reader, out io.Writer, rows, cols int, theme Theme) error {
	r := keyReader(in)
	viewRows := rows - 2
	if viewRows < 1 {
		viewRows = 1
	}
	draw(out, Render(m, rows, cols, theme))
	for !m.Quit {
		k, err := ParseKey(r)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		apply(m, k, viewRows)
		draw(out, Render(m, rows, cols, theme))
	}
	return nil
}

// apply dispatches one key to the model per the current mode.
func apply(m *Model, k Key, viewRows int) {
	if m.Mode == ModeComment {
		handleComment(m, k)
	} else {
		handleNormal(m, k, viewRows)
	}
}

// runResizable is Run plus live terminal-resize handling: size is re-queried
// whenever a value arrives on resize, so the gutter line numbers stay aligned
// with the code after a SIGWINCH (a stale size would wrap rows and mis-anchor
// comments). Keys are read in a goroutine so the loop can select between a
// keystroke and a resize; the model is only ever touched from this goroutine.
func runResizable(m *Model, in io.Reader, out io.Writer, size func() (rows, cols int), theme Theme, resize <-chan struct{}) error {
	r := keyReader(in)
	rows, cols := size()
	draw(out, Render(m, rows, cols, theme))

	type keyEvent struct {
		k   Key
		err error
	}
	keys := make(chan keyEvent)
	go func() {
		for {
			k, err := ParseKey(r)
			keys <- keyEvent{k, err}
			if err != nil {
				return
			}
		}
	}()

	for !m.Quit {
		select {
		case e := <-keys:
			if e.err != nil {
				if e.err == io.EOF {
					return nil
				}
				return e.err
			}
			viewRows := rows - 2
			if viewRows < 1 {
				viewRows = 1
			}
			apply(m, e.k, viewRows)
		case <-resize:
			rows, cols = size()
		}
		draw(out, Render(m, rows, cols, theme))
	}
	return nil
}

func handleNormal(m *Model, k Key, viewRows int) {
	switch k.Kind {
	case KeyUp:
		m.Move(-1, viewRows)
	case KeyDown:
		m.Move(1, viewRows)
	case KeyPageUp:
		m.Page(-1, viewRows)
	case KeyPageDown:
		m.Page(1, viewRows)
	case KeyHome:
		m.ToStart(viewRows)
	case KeyEnd:
		m.ToEnd(viewRows)
	case KeyCtrlC:
		m.Quit = true
	case KeyRune:
		switch k.Rune {
		case 'k':
			m.Move(-1, viewRows)
		case 'j':
			m.Move(1, viewRows)
		case 'g':
			m.ToStart(viewRows)
		case 'G':
			m.ToEnd(viewRows)
		case 'n':
			m.NextFile(viewRows)
		case 'p':
			m.PrevFile(viewRows)
		case ' ':
			m.ToggleSelect()
		case 'c':
			m.StartComment()
		case 'q':
			m.Quit = true
		}
	}
}

func handleComment(m *Model, k Key) {
	switch k.Kind {
	case KeyEnter:
		m.CommitComment()
	case KeyEsc:
		m.CancelComment()
	case KeyBackspace:
		m.InputBackspace()
	case KeyRune:
		m.InputRune(k.Rune)
	}
}

// draw repaints the frame: home the cursor, then write each line clearing to
// end-of-line so shorter frames don't leave stale text.
func draw(out io.Writer, frame []string) {
	var b []byte
	b = append(b, "\x1b[H"...)
	for i, line := range frame {
		b = append(b, "\x1b[K"...)
		b = append(b, line...)
		if i < len(frame)-1 {
			b = append(b, "\r\n"...)
		}
	}
	out.Write(b)
}

// RunTerminal is the thin, untested wrapper: raw mode + alternate screen
// around Run on the real tty. Everything it depends on (Run, Render, the
// model) is exercised headlessly.
func RunTerminal(m *Model, theme Theme) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("not a terminal")
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	restore := func() {
		term.Restore(fd, oldState)
		fmt.Fprint(os.Stdout, "\x1b[?25h\x1b[?1049l") // show cursor, leave alt screen
	}
	defer restore()

	// A defer doesn't run when the process is killed by a signal, which would
	// leave the shell in raw mode on the alternate screen. Catch the common
	// external signals and restore first. (Ctrl-C in raw mode arrives as a byte,
	// not SIGINT, so this is for SIGTERM/SIGHUP and the like.)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; ok {
			restore()
			os.Exit(130)
		}
	}()

	// SIGWINCH => re-query size and redraw, so a mid-review resize keeps the
	// gutter line numbers aligned with the code (coalesced: a full buffer is
	// fine, the next read still picks up the latest size).
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	resize := make(chan struct{}, 1)
	go func() {
		for range winch {
			select {
			case resize <- struct{}{}:
			default:
			}
		}
	}()

	size := func() (rows, cols int) {
		c, r, err := term.GetSize(fd)
		if err != nil || r < 3 || c < 20 {
			return 24, 80
		}
		return r, c
	}
	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l") // alt screen, hide cursor

	return runResizable(m, os.Stdin, os.Stdout, size, theme, resize)
}
