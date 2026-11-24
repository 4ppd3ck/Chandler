// Package term provides the small terminal helpers the interactive menu
// needs: TTY detection and window size, the ANSI cursor/clear/erase
// sequences used by tui_erase, clear and the \e[?25l/\e[?25h cursor
// toggles in the bash original, the keystroke decoding of reader.go,
// whose Reader.ReadKey mirrors the script's `read -rsn1` plus
// `read -rsn2 -t 0.05` escape disambiguation, and the raw-mode toggle of
// raw.go, whose MakeRaw/Restore give a Go reader the silent,
// char-at-a-time tty that bash gets per-read from -rsn.
//
// Everything is stdlib-only. The per-OS console plumbing lives in its own
// files, selected by build tags:
//
//   - the BSD family (darwin included) and linux: TIOCGWINSZ for the tty
//     probe and window size (term_unix.go, with the per-family request
//     code in term_bsd.go and term_linux.go), termios ioctls for raw mode
//     (raw_bsd.go, raw_linux.go);
//   - windows: the Console API — GetConsoleMode/SetConsoleMode for the
//     tty probe and raw mode (term_windows.go, raw_windows.go over the
//     shared plumbing in console_windows.go), GetConsoleScreenBufferInfo
//     for the size, and an output-mode VT enable so the ANSI sequences
//     render on classic conhost;
//   - everything else: raw mode reports a clear unsupported error
//     (raw_other.go) and callers fall back to non-interactive mode.
package term

import (
	"fmt"
	"io"
	"os"
)

const (
	defaultCols  = 80
	defaultLines = 24
)

func IsTTY(fd uintptr) bool {
	return isTTY(fd)
}

func Size(fd uintptr) (cols, lines int, err error) {
	return termSize(fd)
}

const (
	hideCursorSeq = "\x1b[?25l"
	showCursorSeq = "\x1b[?25h"
	clearSeq      = "\x1b[H\x1b[2J" + "\x1b[H"
	enterAltSeq   = "\x1b[?1049h"
	leaveAltSeq   = "\x1b[?1049l"
)

func HideCursor(w io.Writer) {
	fmt.Fprint(w, hideCursorSeq)
}

func ShowCursor(w io.Writer) {
	fmt.Fprint(w, showCursorSeq)
}

func ClearScreen(w io.Writer) {
	fmt.Fprint(w, clearSeq)
}

// EnterAltScreen switches the terminal to the alternate screen buffer —
// the fzf pattern: the menu lives on a scratch screen the user's
// scrollback never sees, and LeaveAltScreen restores the pre-menu screen
// byte-for-byte. The clear after the switch is belt and braces (the
// alternate screen starts empty) — some emulators preserve its previous
// content. Terminals without 1049 support ignore the sequence and keep
// the historical draw-over-the-screen behavior.
func EnterAltScreen(w io.Writer) {
	fmt.Fprint(w, enterAltSeq)
	ClearScreen(w)
}

// LeaveAltScreen returns to the primary screen buffer, discarding
// everything the menu drew. Action output was printed on the primary
// screen while the action ran (the menu leaves the buffer for those),
// so it stays in the user's scrollback.
func LeaveAltScreen(w io.Writer) {
	fmt.Fprint(w, leaveAltSeq)
}

func EraseBlock(n int) {
	eraseBlock(os.Stdout, n)
}

func eraseBlock(w io.Writer, n int) {
	fmt.Fprintf(w, "\x1b[%dA", n)
	for i := 0; i < n; i++ {
		fmt.Fprint(w, "\r\x1b[2K\n")
	}
	fmt.Fprintf(w, "\x1b[%dA", n)
}
