// draw.go is the Go port of the TUI's drawing half in the bash original:
// tui_geometry()'s size probing (the tput 80x24 fallback and the 78-column
// cap) and tui_draw()'s menu block — the ASCII-art logo or the compact 🚢
// header, the status line, the 14 group-colored item rows, the ━
// separator, the key hints and the footer. Like the bash version Draw
// writes every line as "\r\x1b[2K" + content + "\n" and reports the block
// height so the caller can erase and redraw in place (term.EraseBlock).

package menu

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"chandler/internal/term"
)

const (
	fallbackCols  = 80
	fallbackLines = 24
	maxCols       = 78
	LabelW        = 22
	erasePrefix   = "\r\x1b[2K"
)

var logoLines = []string{
	"                   |    |    |",
	"                  )_)  )_)  )_)",
	"                 )___))___))___)\\",
	"                )____)____)_____)\\\\",
	"            _______|____|____|_______",
	"  ~~~~~~~~~~~\\_______________________/~~~~~~~~~~~",
}

// menuOverhead counts the lines Draw renders besides the item rows —
// header, blank, status, blank, blank, separator, hints, footer. With it,
// BlockHeight is the exact height of the block Draw will produce, which
// the TUI gate requires the terminal to hold and the logo decision
// derives from (the logo adds len(logoLines) lines over the 1-line
// compact header). Derived, so adding a menu entry or a logo line moves
// the gates instead of leaving the thresholds behind the real height.
const menuOverhead = 8

// BlockHeight returns the height of the menu block at the given
// show-logo setting for the current Items.
func BlockHeight(showLogo bool) int {
	h := len(Items) + menuOverhead
	if showLogo {
		h += len(logoLines)
	}
	return h
}

// MinLines is the terminal height that shows the block whole: one row
// more than its height. Every drawn line ends in a newline, so on a
// terminal of exactly the block's height the last one scrolls the top
// line off the screen.
func MinLines(showLogo bool) int {
	return BlockHeight(showLogo) + 1
}

func Geometry(w io.Writer) (cols, lines int, showLogo bool) {
	cols, lines = fallbackCols, fallbackLines
	if f, ok := w.(*os.File); ok {
		if c, l, err := term.Size(f.Fd()); err == nil {
			cols, lines = c, l
		}
	}
	if cols > maxCols {
		cols = maxCols
	}
	return cols, lines, lines >= MinLines(true)
}

// Draw renders the menu block and returns its height. The checked map
// drives the multi-select's ✓ column: installer-backed entries the user
// space-checked get a green check between the number and the label; the
// bulk/config/update/uninstall entries have no check column content.
// The installed map marks entries whose tool is already present — a
// dim `· installed` suffix on the description, plus the installed count
// in the status line. prereqMissing is the prerequisite names the
// startup check could not provide — an honest ⚠ list instead of an
// unconditional ✓, so the menu never claims readiness the installers
// will not have.
func Draw(w io.Writer, sel int, cols int, showLogo bool, osName, pkgMgr string, c Colors, checked map[int]bool, installed map[string]bool, prereqMissing []string) int {
	var out []string

	if showLogo {
		for _, line := range logoLines {
			out = append(out, c.BMagenta+line+c.Reset)
		}
		out = append(out, c.Magenta+"                 C H A N D L E R"+c.Reset)
	} else {
		out = append(out, c.BMagenta+"🚢 chandler"+c.Reset)
	}
	out = append(out, "")

	pkgPart := "pkg: none"
	if pkgMgr != "" {
		pkgPart = "pkg: " + pkgMgr
	}
	count, total := installedCount(installed)
	var prereqPart string
	if len(prereqMissing) == 0 {
		prereqPart = c.BGreen + "✓ prerequisites ready" + c.Reset
	} else {
		prereqPart = c.BYellow + "⚠ prerequisites missing: " + strings.Join(prereqMissing, ", ") + c.Reset
	}
	line := prereqPart + " · " + c.Green + fmt.Sprintf("%d/%d tools installed", count, total) + c.Reset
	// The os/pkg tail is the one part that gives way when the line runs
	// long — a long missing list costs it, never the warning itself.
	osPart := c.Dim + "os: " + osName + " · " + pkgPart + c.Reset
	if visibleWidth(line)+3+visibleWidth(osPart) <= cols {
		line += "   " + osPart
	}
	out = append(out, line)
	out = append(out, "")

	for i, item := range Items {
		numstr := fmt.Sprintf("%2d", i+1)
		check := " "
		if checked != nil && checked[i] {
			check = c.BGreen + "✓" + c.Reset
		}
		var marker, label, desc string
		if i == sel {
			marker = c.BMagenta + "▶" + c.Reset
			label = c.Bold + item.Label + c.Reset
			desc = c.Gray + item.Desc + installedMark(item.Action, installed, c)
			numstr = c.BMagenta + numstr + c.Reset
		} else {
			marker = " "
			label = groupColor(c, item.Group) + item.Label + c.Reset
			desc = c.Dim + item.Desc + c.Reset + installedMark(item.Action, installed, c)
			numstr = c.Dim + numstr + c.Reset
		}
		pad := LabelW - utf8.RuneCountInString(item.Label)
		if pad < 1 {
			pad = 1
		}
		out = append(out, fmt.Sprintf("%s %s %s  %s%*s%s", marker, numstr, check, label, pad, "", desc))
	}

	out = append(out, "")
	out = append(out, c.Magenta+strings.Repeat("━", cols)+c.Reset)
	out = append(out, hints(c))
	// With space-checked entries the footer says what ⏎ will do — the
	// checks are otherwise invisible until the key is pressed.
	footer := c.Dim + "chandler · interactive menu" + c.Reset
	if len(checked) > 0 {
		footer = c.BGreen + fmt.Sprintf("⏎ runs %d checked", len(checked)) + c.Reset +
			c.Dim + " · chandler · interactive menu" + c.Reset
	}
	out = append(out, footer)

	for _, line := range out {
		// Every line is cut to the layout width: a wrapped line takes two
		// terminal rows while the returned height counts one, and every
		// later erase/redraw would drift by the difference.
		fmt.Fprint(w, erasePrefix+fitLine(line, cols, c.Reset)+"\n")
	}
	return len(out)
}

// visibleWidth counts the printable runes of s, escape sequences
// skipped — the cells the line takes up on single-width runes (the one
// emoji in use sits in a short line).
func visibleWidth(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = escapeEnd(s, i)
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// escapeEnd returns the index just past the escape sequence starting at
// s[i]: a CSI sequence (ESC [ parameter bytes final byte — the only kind
// the drawing code emits) or, for safety, any other ESC plus one byte.
func escapeEnd(s string, i int) int {
	i++ // ESC
	if i >= len(s) {
		return i
	}
	if s[i] != '[' {
		return i + 1
	}
	for i++; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x3f {
			return i + 1 // the final byte
		}
	}
	return i
}

// fitLine returns s when it fits width printable runes, else its first
// width runes with every escape sequence kept and reset appended — an
// interrupted color must not leak into the next line.
func fitLine(s string, width int, reset string) string {
	if visibleWidth(s) <= width {
		return s
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			end := escapeEnd(s, i)
			b.WriteString(s[i:end])
			i = end
			continue
		}
		if n == width {
			break
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+size])
		i += size
		n++
	}
	// An interrupted color must not leak into the next line; a cut that
	// landed right on the line's own reset must not double it.
	out := b.String()
	if !strings.HasSuffix(out, reset) {
		out += reset
	}
	return out
}

// installedCount counts the installer-backed entries whose probe came
// back positive. nil map (a probe-less render): zero of the known total.
func installedCount(installed map[string]bool) (count, total int) {
	total = len(installProbes)
	for _, ok := range installed {
		if ok {
			count++
		}
	}
	return count, total
}

// installedMark renders the `· installed` suffix for an installer-backed
// entry whose tool is already present; everything else renders nothing.
func installedMark(action string, installed map[string]bool, c Colors) string {
	if installed == nil || !installed[action] {
		return ""
	}
	return "  " + c.Green + "· installed" + c.Reset
}

func groupColor(c Colors, group string) string {
	switch group {
	case "k8s":
		return c.Green
	case "cloud":
		return c.Blue
	case "app":
		return c.Yellow
	case "setup":
		return c.Magenta
	case "bulk":
		return c.Cyan
	case "danger":
		return c.BRed
	default:
		return ""
	}
}

// hints is the one-line key summary under the separator; the ? overlay
// carries the rest. Two-space gaps keep it inside the 78-column layout.
func hints(c Colors) string {
	parts := [][2]string{
		{"↑↓", "navigate"}, {"⏎", "run"}, {"␣", "check"}, {"1-9", "select"},
		{"c", "config"}, {"u", "update"}, {"?", "keys"}, {"q", "quit"},
	}
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(c.BMagenta + part[0] + c.Reset + " " + part[1])
	}
	return b.String()
}

// helpRows is the ? overlay's key list — everything the one-line hint
// bar cannot fit: the vim keys, Home/End, the quit keys, the space-check
// semantics and the two-digit compose.
var helpRows = []struct{ keys, what string }{
	{"↑↓ / j k", "move"},
	{"g / Home", "first entry"},
	{"G / End", "last entry"},
	{"⏎", "run the highlighted entry — or every checked one, in order"},
	{"␣", "check / uncheck an entry for a multi-tool run"},
	{"1-9, 0", "go to entry 1-10 · the same key again runs it"},
	{"1 then 2-5", "go to entry 12-15"},
	{"c", "config: stacks & cloud"},
	{"u", "update installed scripts"},
	{"?", "this help"},
	{"q / Ctrl-D / Ctrl-C", "quit"},
}

// drawHelp renders the ? overlay from the top of the screen and returns
// its height. Same line protocol as Draw (erasePrefix per line), so a
// ClearScreen after it is all the cleanup the caller needs.
func drawHelp(w io.Writer, c Colors) int {
	keyW := 0
	for _, row := range helpRows {
		keyW = max(keyW, utf8.RuneCountInString(row.keys))
	}
	var out []string
	out = append(out, c.BMagenta+"keys"+c.Reset)
	out = append(out, "")
	for _, row := range helpRows {
		pad := keyW - utf8.RuneCountInString(row.keys) + 2
		out = append(out, "  "+c.BMagenta+row.keys+c.Reset+fmt.Sprintf("%*s", pad, "")+c.Gray+row.what+c.Reset)
	}
	out = append(out, "")
	out = append(out, c.Dim+"press any key to close"+c.Reset)
	for _, line := range out {
		fmt.Fprint(w, erasePrefix+line+"\n")
	}
	return len(out)
}
