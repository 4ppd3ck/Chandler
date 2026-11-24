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
	// The gate matches the block Draw produces (MinLines), not a guess:
	// on a terminal too short for it the block's top lines scroll off,
	// and the highlight can move to a row nobody sees — the numeric
	// fallback menu serves those instead.
	return cols >= 60 && lines >= MinLines(false)
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

// TuiLoop runs the interactive menu. prereqMissing carries the
// prerequisite names the startup check could not satisfy — the status
// line shows them as a ⚠ list instead of claiming readiness.
func TuiLoop(osName, pkgMgr, root, scriptDir string, prereqMissing []string, rd *term.Reader, initialSel int) {
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
	// The menu lives on the alternate screen buffer: entering here hides
	// it from the user's scrollback, leaving on every exit path (quit,
	// Ctrl-D, signal, successful uninstall) restores the pre-menu screen
	// byte-for-byte. Actions temporarily leave the buffer — their output
	// belongs in the scrollback, like a plain command run.
	term.EnterAltScreen(os.Stdout)
	defer func() {
		term.Restore(state.Load())
		term.ShowCursor(os.Stdout)
		term.LeaveAltScreen(os.Stdout)
		SaveLastSelection(int(selAtomic.Load()))
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		<-sig
		term.Restore(state.Load())
		term.ShowCursor(os.Stdout)
		term.LeaveAltScreen(os.Stdout)
		os.Exit(1)
	}()

	term.HideCursor(os.Stdout)

	colors := NewColors(ColorsEnabled(os.Stdout))
	n := len(Items)
	sel := max(0, min(initialSel, n-1))
	selAtomic.Store(int32(sel))
	checked := make(map[int]bool)
	// What this machine already has: the probe runs once here and again
	// after every action, so the rows' · installed markers and the status
	// line's count track reality through the session.
	installed := InstalledTools()

	// The TUI's confirms read one raw keypress instead of the typed
	// [y/N] line — same printed prompt, TUI-grade answer. nil leaves the
	// runner on the typed read (fallback menu, `chandler tool`). The one
	// program-wide keystroke Reader serves the confirm too.
	SingleKeyConfirm = func(prompt string) bool { return singleKeyConfirm(rd, prompt) }
	defer func() { SingleKeyConfirm = nil }()

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

	// syncGeometry re-probes the terminal (under drawMu) and reports
	// whether its size changed since the last draw, updating the layout
	// to match: cols keeps Geometry's 78-column cap, and the logo shows
	// exactly when the block with it still fits. A caller that gets true
	// clears the screen instead of erasing in place — the block on screen
	// no longer has the height recorded in blockHeight.
	syncGeometry := func() bool {
		if c, l, err := term.Size(os.Stdout.Fd()); err == nil && (min(c, maxCols) != cols || l != lines) {
			cols, lines = min(c, maxCols), l
			showLogo = lines >= MinLines(true)
			return true
		}
		return false
	}

	blockHeight := Draw(os.Stdout, sel, cols, showLogo, osName, pkgMgr, colors, checked, installed, prereqMissing)

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
		if syncGeometry() {
			term.ClearScreen(os.Stdout)
			blockHeight = Draw(os.Stdout, int(selAtomic.Load()), cols, showLogo, osName, pkgMgr, colors, checked, installed, prereqMissing)
		}
		drawMu.Unlock()
	})
	defer stopResize()

	// Every redraw re-probes the size. The watcher stands down while an
	// action owns the screen, so a resize during an installer would
	// otherwise go unhandled until the next keypress — with the menu
	// drawn for the old geometry the whole time.
	redraw := func(sel int) {
		selAtomic.Store(int32(sel))
		drawMu.Lock()
		if syncGeometry() {
			term.ClearScreen(os.Stdout)
		} else {
			term.EraseBlock(blockHeight)
		}
		blockHeight = Draw(os.Stdout, sel, cols, showLogo, osName, pkgMgr, colors, checked, installed, prereqMissing)
		drawMu.Unlock()
	}

	waitKey := func() {
		_, _ = rd.ReadKey()
	}

	// returnToMenu ends a primary-screen interlude (an action, the config
	// and update handlers): one keypress is held on that screen — where
	// the interlude's output and this prompt are both visible, since the
	// alternate screen would hide the output — and only then is the
	// menu's own screen brought back (cleared) for the redraw that
	// follows. The caller has put the terminal back in raw mode.
	returnToMenu := func() {
		fmt.Printf("\r\n%s─ press any key to return to the menu ─%s", colors.Dim, colors.Reset)
		waitKey()
		term.EnterAltScreen(os.Stdout)
	}

	// runSelection runs the selected action and reports whether the session
	// should end afterwards: the uninstall entry removes the app itself, so
	// returning to the menu makes no sense — on success its output stays on
	// screen and the terminal is left cooked (the loop's deferred Restore
	// puts the original state back as the session exits). A FAILED uninstall
	// keeps the menu, like any other failed action.
	//
	// The alternate screen is left for the action's duration: the ▶ header
	// and the installer output land on the primary screen, below where the
	// user started chandler — scrollback they keep after the menu is gone.
	runSelection := func(sel int) bool {
		actionRunning.Store(true)
		defer actionRunning.Store(false)
		drawMu.Lock()
		term.EraseBlock(blockHeight)
		drawMu.Unlock()
		term.LeaveAltScreen(os.Stdout)
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
		returnToMenu()
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
		term.LeaveAltScreen(os.Stdout)
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
		returnToMenu()
	}

loop:
	for {
		key, err := rd.ReadKey()
		if err != nil {
			break loop
		}
		// Resize noticed on a keypress (also the fallback when the
		// watcher is not running): redraw under the same lock it uses.
		drawMu.Lock()
		resized := syncGeometry()
		if resized {
			term.ClearScreen(os.Stdout)
			blockHeight = Draw(os.Stdout, sel, cols, showLogo, osName, pkgMgr, colors, checked, installed, prereqMissing)
		}
		drawMu.Unlock()
		if resized {
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
		case term.KeyCtrlC:
			// Raw mode keeps ISIG off, so Ctrl-C arrives here as a key,
			// not a signal — and the signal handler only covers the
			// cooked moments. Quit like q: the reflex must work.
			break loop
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
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				d := digitIndex(key.Rune)
				if d >= n {
					break
				}
				// "1" can also be the first half of a two-digit entry: on
				// a menu long enough to have one, wait a moment for the
				// second digit and compose when the pair names an entry.
				// Anything else — a repeated digit, a pair with no entry,
				// another key — comes back on the next read untouched, so
				// the single-digit behavior (press again to run) holds.
				if canStartCompose(key.Rune, n) {
					pair := func(second rune) bool {
						_, ok := composeEntry(key.Rune, second, n)
						return ok
					}
					if second, ok, _ := rd.ReadTailDigit(pair); ok {
						d, _ = composeEntry(key.Rune, second, n)
					}
				}
				if d == sel {
					if runSelection(sel) {
						return
					}
				}
				sel = d
				redraw(sel)
			case '?':
				// The key overlay: the one-line hint bar cannot fit the
				// vim keys, the quit keys or the two-digit compose. The
				// watcher stands down (actionRunning) so a resize cannot
				// draw the menu over the help.
				actionRunning.Store(true)
				drawMu.Lock()
				term.EraseBlock(blockHeight)
				drawMu.Unlock()
				term.ClearScreen(os.Stdout)
				drawHelp(os.Stdout, colors)
				waitKey()
				term.ClearScreen(os.Stdout)
				actionRunning.Store(false)
				redraw(sel)
			case 'c', 'C':
				actionRunning.Store(true)
				drawMu.Lock()
				term.EraseBlock(blockHeight)
				drawMu.Unlock()
				term.LeaveAltScreen(os.Stdout)
				term.ShowCursor(os.Stdout)
				term.Restore(state.Load())
				if _, err := ask.AskStackCloud(root, rd); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
				if ns, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
					state.Store(ns)
				}
				term.HideCursor(os.Stdout)
				returnToMenu()
				actionRunning.Store(false)
				redraw(sel)
			case 'u', 'U':
				actionRunning.Store(true)
				drawMu.Lock()
				term.EraseBlock(blockHeight)
				drawMu.Unlock()
				term.LeaveAltScreen(os.Stdout)
				term.ShowCursor(os.Stdout)
				term.Restore(state.Load())
				_ = UpdateScripts()
				drawMu.Lock()
				refreshInstalled(installed)
				drawMu.Unlock()
				if ns, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
					state.Store(ns)
				}
				term.HideCursor(os.Stdout)
				returnToMenu()
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

// digitIndex is the entry a single digit selects: '1'-'9' are entries
// 1-9, '0' is entry 10.
func digitIndex(d rune) int {
	if d == '0' {
		return 9
	}
	return int(d-'0') - 1
}

// composeEntry reports whether the keystroke pair first, second names an
// entry of the n-entry menu, and which one: only "1" followed by another
// digit can reach past entry 10 ("12" is entry 12). A repeated digit is
// never a pair — the same key twice is press-again-to-run at any speed —
// and a pair that names no entry is no pair, so both answer 0, false.
func composeEntry(first, second rune, n int) (int, bool) {
	if first != '1' || second < '0' || second > '9' || second == first {
		return 0, false
	}
	idx := 9 + int(second-'0') // "12" → entry 12 → index 11
	if idx >= n {
		return 0, false
	}
	return idx, true
}

// canStartCompose reports whether a keystroke after first could turn it
// into a two-digit selection — the menu must have an entry past 10 and
// first must be the digit such entries start with.
func canStartCompose(first rune, n int) bool {
	return first == '1' && n > 10
}

// singleKeyConfirm is the TUI's confirm: the prompt the typed fallback
// prints, answered with ONE raw keypress through the loop's Reader — y/Y
// runs, anything else (Enter included, so a stray key can never confirm)
// aborts. The action path leaves the terminal cooked before the runner
// asks, so raw mode is taken for this one read and given straight back;
// where raw mode is unavailable the typed line answers, as everywhere
// else. The pressed key is echoed, so the transcript shows the answer.
func singleKeyConfirm(rd *term.Reader, prompt string) bool {
	fmt.Print(prompt)
	saved, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		return typedAnswer()
	}
	key, keyErr := rd.ReadKey()
	term.Restore(saved)
	ok := keyErr == nil && key.Type == term.KeyRune && (key.Rune == 'y' || key.Rune == 'Y')
	switch {
	case ok, keyErr == nil && key.Type == term.KeyRune:
		fmt.Printf("%c\n", key.Rune)
	default:
		fmt.Println()
	}
	if !ok {
		fmt.Println("Aborted.")
	}
	return ok
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
