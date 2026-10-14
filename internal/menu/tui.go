// tui.go is the Go port of the interactive half of the bash original's
// TUI: tui_use_tui()'s gate, tui_loop()'s key loop, tui_run()'s action
// dispatch and fallback_numeric_menu()'s plain prompt. The drawing half —
// tui_geometry/tui_draw/tui_erase and setup_colors — lives in draw.go and
// colors.go, the action runner (run_menu_action + run_install) in
// runner.go.
//
// Where bash is only ever raw for the duration of one `read -rsn1`, the
// port keeps fd 0 in raw mode across the whole loop (term.MakeRaw) and
// hands the terminal back exactly where bash would have it: cooked for
// everything that prints or reads lines (RunAction, update-scripts.sh,
// ask.AskStackCloud), raw again for the single-keystroke pauses. Because
// raw mode also clears OPOST/onlcr, every newline printed while raw is
// written as "\r\n" — the two bytes the line discipline turns bash's bare
// "\n" printf output into — so the layout matches the bash TUI's.
//
// TuiLoop is inherently interactive and needs a real terminal on fds 0 and 1.

package menu

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"chandler/internal/ask"
	"chandler/internal/term"
)

func UseTUI() bool {
	if !term.IsTTY(os.Stdin.Fd()) || !term.IsTTY(os.Stdout.Fd()) {
		return false
	}
	cols, lines, err := term.Size(os.Stdout.Fd())
	if err != nil {
		return false
	}
	return cols >= 60 && lines >= 16
}

func TuiLoop(osName, pkgMgr, root, scriptDir string, rd *term.Reader) {
	cols, lines, showLogo := Geometry(os.Stdout)

	var state atomic.Pointer[term.State]

	saved, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		FallbackMenu(osName, pkgMgr, root, scriptDir)
		return
	}
	state.Store(saved)
	defer func() {
		term.Restore(state.Load())
		term.ShowCursor(os.Stdout)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		<-sig
		term.Restore(state.Load())
		term.ShowCursor(os.Stdout)
		os.Exit(1)
	}()

	term.HideCursor(os.Stdout)
	term.ClearScreen(os.Stdout)

	colors := NewColors(ColorsEnabled(os.Stdout))
	n := len(Items)
	sel := 0
	blockHeight := Draw(os.Stdout, sel, cols, showLogo, osName, pkgMgr, colors)

	redraw := func(sel int) {
		term.EraseBlock(blockHeight)
		blockHeight = Draw(os.Stdout, sel, cols, showLogo, osName, pkgMgr, colors)
	}

	waitKey := func() {
		_, _ = rd.ReadKey()
	}

	runSelection := func(sel int) {
		term.EraseBlock(blockHeight)
		fmt.Printf("\r\x1b[2K%s▶%s %s%s%s\r\n\r\n",
			colors.BMagenta, colors.Reset, colors.Bold, Items[sel].Label, colors.Reset)
		term.ShowCursor(os.Stdout)
		term.Restore(state.Load())
		_ = RunAction(Items[sel].Action, root, scriptDir, rd)
		if ns, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
			state.Store(ns)
		}
		term.HideCursor(os.Stdout)
		fmt.Printf("\r\n%s─ press any key to return to the menu ─%s", colors.Dim, colors.Reset)
		waitKey()
		fmt.Printf("\r\n")
		term.ClearScreen(os.Stdout)
	}

loop:
	for {
		key, err := rd.ReadKey()
		if err != nil {
			break loop
		}
		if c, l, err := term.Size(os.Stdout.Fd()); err == nil && (c != cols || l != lines) {
			cols, lines = c, l
			term.ClearScreen(os.Stdout)
			blockHeight = Draw(os.Stdout, sel, cols, showLogo, osName, pkgMgr, colors)
			continue
		}
		switch key.Type {
		case term.KeyUp:
			sel = (sel - 1 + n) % n
			redraw(sel)
		case term.KeyDown:
			sel = (sel + 1) % n
			redraw(sel)
		case term.KeyHome:
			sel = 0
			redraw(sel)
		case term.KeyEnd:
			sel = n - 1
			redraw(sel)
		case term.KeyEnter:
			runSelection(sel)
			redraw(sel)
		case term.KeyCtrlD:
			break loop
		case term.KeyRune:
			switch key.Rune {
			case 'j', 'J':
				sel = (sel + 1) % n
				redraw(sel)
			case 'k', 'K':
				sel = (sel - 1 + n) % n
				redraw(sel)
			case 'g':
				sel = 0
				redraw(sel)
			case 'G':
				sel = n - 1
				redraw(sel)
			case '1', '2', '3', '4', '5', '6', '7', '8', '9':
				if d := int(key.Rune-'0') - 1; d == sel {
					runSelection(sel)
					redraw(sel)
				} else {
					sel = d
					redraw(sel)
				}
			case '0':
				if sel == 9 {
					runSelection(sel)
					redraw(sel)
				} else {
					sel = 9
					redraw(sel)
				}
			case 'c', 'C':
				term.EraseBlock(blockHeight)
				term.Restore(state.Load())
				if _, err := ask.AskStackCloud(root, rd); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
				if ns, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
					state.Store(ns)
				}
				fmt.Printf("\r\n%spress any key to return%s", colors.Dim, colors.Reset)
				waitKey()
				term.ClearScreen(os.Stdout)
				redraw(sel)
			case 'u', 'U':
				term.EraseBlock(blockHeight)
				term.Restore(state.Load())
				_ = UpdateScripts()
				if ns, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
					state.Store(ns)
				}
				fmt.Printf("\r\n%spress any key to return%s", colors.Dim, colors.Reset)
				waitKey()
				term.ClearScreen(os.Stdout)
				redraw(sel)
			case 'q', 'Q':
				break loop
			}
		}
	}

	term.EraseBlock(blockHeight)
}

func FallbackMenu(osName, pkgMgr, root, scriptDir string) {
	fmt.Println("Select an option:")
	for i, item := range Items {
		fmt.Printf("  %2d - %s\n", i+1, item.Label)
	}
	fmt.Println()
	// bash `read -rp` shows the prompt on standard error, not stdout.
	fmt.Fprint(os.Stderr, "Enter option: ")
	line, _ := readLine(os.Stdin)
	option := strings.TrimSpace(line)

	sel, ok := numericSelection(option)
	if !ok {
		fmt.Printf("Invalid option: '%s'.\n", option)
		os.Exit(1)
		// Unreachable under the real os.Exit.
		return
	}
	_ = RunAction(Items[sel].Action, root, scriptDir, nil)
}

func numericSelection(option string) (int, bool) {
	for i := range Items {
		if option == strconv.Itoa(i+1) {
			return i, true
		}
	}
	return 0, false
}
