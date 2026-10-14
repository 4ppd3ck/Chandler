// colors.go ports setup_colors() from the bash original: the C_* ANSI
// variables the TUI paints with, kept byte-for-byte, and the same
// `[[ -t 1 && -z "${NO_COLOR:-}" ]]` gate that decides whether they are
// set at all. With colors disabled every field is the empty string, so the
// printf formats of tui_draw() degrade to plain text exactly the way the
// bash ones do.

package menu

import (
	"io"
	"os"

	"chandler/internal/term"
)

type Colors struct {
	Reset    string
	Bold     string
	Dim      string
	Magenta  string
	BMagenta string
	Green    string
	Blue     string
	Yellow   string
	Cyan     string
	Gray     string
	White    string
	BGreen   string
	BRed     string
	BYellow  string
}

func NewColors(enabled bool) Colors {
	if !enabled {
		return Colors{}
	}
	return Colors{
		Reset:    "\x1b[0m",
		Bold:     "\x1b[1m",
		Dim:      "\x1b[2m",
		Magenta:  "\x1b[35m",
		BMagenta: "\x1b[1;35m",
		Green:    "\x1b[32m",
		Blue:     "\x1b[34m",
		Yellow:   "\x1b[33m",
		Cyan:     "\x1b[36m",
		Gray:     "\x1b[90m",
		White:    "\x1b[97m",
		BGreen:   "\x1b[1;32m",
		BRed:     "\x1b[1;31m",
		BYellow:  "\x1b[1;33m",
	}
}

func ColorsEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTTY(f.Fd())
}
