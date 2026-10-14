package ask

import (
	"errors"
	"fmt"
	"io"
	"os"

	"chandler/internal/term"
)

var ErrCancelled = errors.New("cancelled")

type MultiSelect struct {
	Title  string
	Prompt string
	Items  []string
}

const (
	ansiBoldReverse = "\x1b[1;7m"
	ansiReset       = "\x1b[0m"
)

const erasePrefix = "\r\x1b[2K"

func (m *MultiSelect) Run(rd *term.Reader) ([]string, error) {
	out := os.Stdout

	state, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		return nil, err
	}
	defer term.Restore(state)
	defer term.ShowCursor(out)
	term.HideCursor(out)

	cursor := 0
	checked := make(map[int]bool)
	lines := m.render(out, cursor, checked)

	redraw := func() {
		term.EraseBlock(lines)
		lines = m.render(out, cursor, checked)
	}

	for {
		key, err := rd.ReadKey()
		if err != nil {
			term.EraseBlock(lines)
			if errors.Is(err, io.EOF) {
				return nil, ErrCancelled
			}
			return nil, err
		}

		switch {
		case key.Type == term.KeyUp || isRune(key, 'k'):
			if n := len(m.Items); n > 0 {
				cursor = (cursor - 1 + n) % n
			}
			redraw()
		case key.Type == term.KeyDown || isRune(key, 'j'):
			if n := len(m.Items); n > 0 {
				cursor = (cursor + 1) % n
			}
			redraw()
		case key.Type == term.KeySpace:
			if len(m.Items) > 0 {
				checked[cursor] = !checked[cursor]
			}
			redraw()
		case key.Type == term.KeyEnter:
			term.EraseBlock(lines)
			return m.selection(checked), nil
		case key.Type == term.KeyEscape || isRune(key, 'q') ||
			key.Type == term.KeyCtrlC || key.Type == term.KeyCtrlD:
			term.EraseBlock(lines)
			return nil, ErrCancelled
		}
	}
}

func isRune(key term.KeyMsg, r rune) bool {
	return key.Type == term.KeyRune && key.Rune == r
}

func (m *MultiSelect) selection(checked map[int]bool) []string {
	selected := make([]string, 0, len(checked))
	for i, item := range m.Items {
		if checked[i] {
			selected = append(selected, item)
		}
	}
	return selected
}

func (m *MultiSelect) render(w io.Writer, cursor int, checked map[int]bool) int {
	color := os.Getenv("NO_COLOR") == "" && term.IsTTY(os.Stdout.Fd())

	fmt.Fprint(w, erasePrefix+m.Title+"\n")
	fmt.Fprint(w, erasePrefix+"\n")
	fmt.Fprint(w, erasePrefix+m.Prompt+"\n")

	for i, item := range m.Items {
		marker := " "
		if i == cursor {
			marker = ">"
		}
		box := "[ ]"
		if checked[i] {
			box = "[x]"
		}
		line := fmt.Sprintf("%s %s %s", marker, box, item)
		if i == cursor && color {
			line = ansiBoldReverse + line + ansiReset
		}
		fmt.Fprint(w, erasePrefix+line+"\n")
	}

	return 3 + len(m.Items)
}
