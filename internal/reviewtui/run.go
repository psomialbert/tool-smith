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
		if m.Mode == ModeComment {
			handleComment(m, k)
		} else {
			handleNormal(m, k, viewRows)
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

	cols, rows, err := term.GetSize(fd)
	if err != nil || rows < 3 || cols < 20 {
		rows, cols = 24, 80
	}
	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l") // alt screen, hide cursor

	return Run(m, os.Stdin, os.Stdout, rows, cols, theme)
}
