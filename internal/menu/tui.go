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
// Three additions over the bash original: a resize watcher redraws the
// menu the moment the terminal is resized (not just on the next
// keypress); the highlighted entry is remembered across runs; and Space
// checks entries for a hand-picked multi-tool run — ⏎ with checks runs
// them in menu order through the same confirm-and-summary path as the
// bulk actions.
//
// TuiLoop is inherently interactive and needs a real terminal on fds 0 and 1.

package menu

import (
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// checkable reports whether a menu entry participates in the space-check
// multi-select: only the installer-backed entries do — the bulk, config,
// update and uninstall entries have their own flows.
func checkable(action string) bool {
	switch action {
	case "__all", "__core", "__config", "__update_scripts", "__uninstall":
		return false
	}
	return true
}

func TuiLoop(osName, pkgMgr, root, scriptDir string, rd *term.Reader, initialSel int) {
	cols, lines, showLogo := Geometry(os.Stdout)

	var state atomic.Pointer[term.State]
	// The highlighted entry, mirrored atomically for the resize watcher's
	// goroutine and the deferred selection save.
	var selAtomic atomic.Int32

	saved, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		FallbackMenu(osName, pkgMgr, root, scriptDir)
		return
	}
	state.Store(saved)
	defer func() {
		term.Restore(state.Load())
		term.ShowCursor(os.Stdout)
		SaveLastSelection(int(selAtomic.Load()))
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
	sel := max(0, min(initialSel, n-1))
	selAtomic.Store(int32(sel))
	checked := make(map[int]bool)
	// What this machine already has: the probe runs once here and again
	// after every action, so the rows' · installed markers and the status
	// line's count track reality through the session.
	installed := InstalledTools()

	// The draw state (cols, lines, showLogo, blockHeight) and the maps
	// the rows render (checked, installed) are shared with the resize
	// watcher's goroutine: every mutation — the key loop's Space/Enter
	// check updates and every refreshInstalled — goes through drawMu,
	// the same lock the watcher's Draw holds. The watcher additionally
	// stands down while an action or the config/update handlers own the
	// screen (actionRunning, re-checked under the lock), and reads the
	// highlighted entry through the atomic mirror selAtomic to stay
	// race-free against the key loop's sel updates.
	var drawMu sync.Mutex
	var actionRunning atomic.Bool
	blockHeight := Draw(os.Stdout, sel, cols, showLogo, osName, pkgMgr, colors, checked, installed)

	stopResize := startResizeWatcher(func() {
		drawMu.Lock()
		// actionRunning is re-checked under the lock: checking before it
		// would leave a window where the key loop has already started an
		// action's screen take-over (and its refreshInstalled) while the
		// watcher is still drawing from the maps the action mutates.
		if actionRunning.Load() {
			drawMu.Unlock()
			return
		}
		if c, l, err := term.Size(os.Stdout.Fd()); err == nil && (c != cols || l != lines) {
			cols, lines = c, l
			showLogo = lines >= logoMinLines
			term.ClearScreen(os.Stdout)
			blockHeight = Draw(os.Stdout, int(selAtomic.Load()), cols, showLogo, osName, pkgMgr, colors, checked, installed)
		}
		drawMu.Unlock()
	})
	defer stopResize()

	redraw := func(sel int) {
		selAtomic.Store(int32(sel))
		drawMu.Lock()
		term.EraseBlock(blockHeight)
		blockHeight = Draw(os.Stdout, sel, cols, showLogo, osName, pkgMgr, colors, checked, installed)
		drawMu.Unlock()
	}

	waitKey := func() {
		_, _ = rd.ReadKey()
	}

	// runSelection runs the selected action and reports whether the session
	// should end afterwards: the uninstall entry removes the app itself, so
	// returning to the menu makes no sense — on success its output stays on
	// screen and the terminal is left cooked (the loop's deferred Restore
	// puts the original state back as the session exits). A FAILED uninstall
	// keeps the menu, like any other failed action.
	runSelection := func(sel int) bool {
		actionRunning.Store(true)
		defer actionRunning.Store(false)
		drawMu.Lock()
		term.EraseBlock(blockHeight)
		drawMu.Unlock()
		fmt.Printf("\r\x1b[2K%s▶%s %s%s%s\r\n\r\n",
			colors.BMagenta, colors.Reset, colors.Bold, Items[sel].Label, colors.Reset)
		term.ShowCursor(os.Stdout)
		term.Restore(state.Load())
		SaveLastSelection(sel)
		var err error
		if Items[sel].Action == "__uninstall" {
			err = RunAction(Items[sel].Action, root, scriptDir, rd)
			if err == nil {
				return true
			}
		} else {
			_ = RunAction(Items[sel].Action, root, scriptDir, rd)
		}
		drawMu.Lock()
		refreshInstalled(installed)
		drawMu.Unlock()
		if ns, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
			state.Store(ns)
		}
		term.HideCursor(os.Stdout)
		fmt.Printf("\r\n%s─ press any key to return to the menu ─%s", colors.Dim, colors.Reset)
		waitKey()
		fmt.Printf("\r\n")
		term.ClearScreen(os.Stdout)
		return false
	}

	// runBatch runs the space-checked entries in menu order — one confirm,
	// one summary — through the same runToolList the bulk actions use.
	runBatch := func(sels []int) {
		actionRunning.Store(true)
		defer actionRunning.Store(false)
		drawMu.Lock()
		term.EraseBlock(blockHeight)
		drawMu.Unlock()
		tools := make([]string, 0, len(sels))
		for _, i := range sels {
			tools = append(tools, Items[i].Action)
		}
		fmt.Printf("\r\x1b[2K%s▶%s %s%s%s\r\n\r\n",
			colors.BMagenta, colors.Reset, colors.Bold,
			fmt.Sprintf("Install %d checked tools", len(sels)), colors.Reset)
		term.ShowCursor(os.Stdout)
		term.Restore(state.Load())
		SaveLastSelection(int(selAtomic.Load()))
		_ = runToolList(tools, false, root, scriptDir)
		drawMu.Lock()
		refreshInstalled(installed)
		drawMu.Unlock()
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
			// Resize noticed on a keypress (also the fallback when the
			// watcher is not running): redraw under the same lock it uses.
			drawMu.Lock()
			cols, lines = c, l
			showLogo = lines >= logoMinLines
			term.ClearScreen(os.Stdout)
			blockHeight = Draw(os.Stdout, sel, cols, showLogo, osName, pkgMgr, colors, checked, installed)
			drawMu.Unlock()
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
		case term.KeySpace:
			if checkable(Items[sel].Action) {
				// Under drawMu: the resize watcher reads this same map
				// inside Draw, on its own goroutine.
				drawMu.Lock()
				if checked[sel] {
					delete(checked, sel)
				} else {
					checked[sel] = true
				}
				drawMu.Unlock()
			}
			redraw(sel)
		case term.KeyEnter:
			// Checked entries win over the highlighted one: run them in
			// menu order, then forget the checks — they were run. The
			// collect and clear sit under drawMu: the resize watcher
			// reads this same map inside Draw, on its own goroutine.
			drawMu.Lock()
			sels := make([]int, 0, len(checked))
			for i := range checked {
				sels = append(sels, i)
			}
			sort.Ints(sels)
			for _, i := range sels {
				delete(checked, i)
			}
			drawMu.Unlock()
			if len(sels) > 0 {
				runBatch(sels)
			} else if runSelection(sel) {
				return
			}
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
					if runSelection(sel) {
						return
					}
					redraw(sel)
				} else {
					sel = d
					redraw(sel)
				}
			case '0':
				if sel == 9 {
					if runSelection(sel) {
						return
					}
					redraw(sel)
				} else {
					sel = 9
					redraw(sel)
				}
			case 'c', 'C':
				actionRunning.Store(true)
				drawMu.Lock()
				term.EraseBlock(blockHeight)
				drawMu.Unlock()
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
				actionRunning.Store(false)
				redraw(sel)
			case 'u', 'U':
				actionRunning.Store(true)
				drawMu.Lock()
				term.EraseBlock(blockHeight)
				drawMu.Unlock()
				term.Restore(state.Load())
				_ = UpdateScripts()
				drawMu.Lock()
				refreshInstalled(installed)
				drawMu.Unlock()
				if ns, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
					state.Store(ns)
				}
				fmt.Printf("\r\n%spress any key to return%s", colors.Dim, colors.Reset)
				waitKey()
				term.ClearScreen(os.Stdout)
				actionRunning.Store(false)
				redraw(sel)
			case 'q', 'Q':
				break loop
			}
		}
	}

	drawMu.Lock()
	term.EraseBlock(blockHeight)
	drawMu.Unlock()
}

func FallbackMenu(osName, pkgMgr, root, scriptDir string) {
	installed := InstalledTools()
	fmt.Println("Select an option:")
	for i, item := range Items {
		mark := ""
		if installed[item.Action] {
			mark = "  (installed)"
		}
		fmt.Printf("  %2d - %s%s\n", i+1, item.Label, mark)
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
