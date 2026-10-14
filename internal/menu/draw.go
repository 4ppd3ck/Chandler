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
	logoMinLines  = 27
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
	return cols, lines, lines >= logoMinLines
}

func Draw(w io.Writer, sel int, cols int, showLogo bool, osName, pkgMgr string, c Colors) int {
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
	out = append(out, c.BGreen+"✓ prerequisites ready"+c.Reset+"   "+
		c.Dim+"os: "+osName+" · "+pkgPart+c.Reset)
	out = append(out, "")

	for i, item := range Items {
		numstr := fmt.Sprintf("%2d", i+1)
		var marker, label, desc string
		if i == sel {
			marker = c.BMagenta + "▶" + c.Reset
			label = c.Bold + item.Label + c.Reset
			desc = c.Gray + item.Desc
			numstr = c.BMagenta + numstr + c.Reset
		} else {
			marker = " "
			label = groupColor(c, item.Group) + item.Label + c.Reset
			desc = c.Dim + item.Desc + c.Reset
			numstr = c.Dim + numstr + c.Reset
		}
		pad := LabelW - utf8.RuneCountInString(item.Label)
		if pad < 1 {
			pad = 1
		}
		out = append(out, fmt.Sprintf("%s %s  %s%*s%s", marker, numstr, label, pad, "", desc))
	}

	out = append(out, "")
	out = append(out, c.Magenta+strings.Repeat("━", cols)+c.Reset)
	out = append(out, hints(c))
	out = append(out, c.Dim+"chandler · interactive menu"+c.Reset)

	for _, line := range out {
		fmt.Fprint(w, erasePrefix+line+"\n")
	}
	return len(out)
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
	default:
		return ""
	}
}

func hints(c Colors) string {
	return c.BMagenta + "↑↓" + c.Reset + " navigate   " +
		c.BMagenta + "⏎" + c.Reset + " run   " +
		c.BMagenta + "1-9" + c.Reset + " select   " +
		c.BMagenta + "c" + c.Reset + " config   " +
		c.BMagenta + "u" + c.Reset + " update   " +
		c.BMagenta + "q" + c.Reset + " quit"
}
