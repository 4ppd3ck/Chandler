// resize_windows.go: the console-resize feed for the TUI. Windows has no
// SIGWINCH — a resize arrives as a WINDOW_BUFFER_SIZE_EVENT record in the
// console input buffer, which term's raw mode already enables
// (enableWindowInput, see internal/term/raw_windows.go). The watcher
// waits on the input handle and consumes ONLY resize records: a key (or
// focus, or menu) record at the front of the queue ends the drain and is
// left for whoever reads stdin next — the TUI's keystroke reader during
// the loop, or the child installer's own prompts while an action runs —
// so the watcher can never eat a keystroke. Anything that fails — a
// non-console stdin like mintty's pipes, a missing entry point — stops
// the watcher silently and the TUI falls back to noticing a resize on
// the next keypress, like the bash original did.

//go:build windows

package menu

import (
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var kernel32Resize = syscall.NewLazyDLL("kernel32.dll")

var (
	procPeekConsoleInputR = kernel32Resize.NewProc("PeekConsoleInputW")
	procReadConsoleInputR = kernel32Resize.NewProc("ReadConsoleInputW")
	procWaitForObjectR    = kernel32Resize.NewProc("WaitForSingleObject")
	procGetConsoleModeR   = kernel32Resize.NewProc("GetConsoleMode")
	procSetConsoleModeR   = kernel32Resize.NewProc("SetConsoleMode")
)

const (
	// INPUT_RECORD.EventType for a console screen-buffer resize, and the
	// console input mode that makes the buffer generate them.
	windowBufferSizeEvent = 0x0004
	enableWindowInputWin  = 0x0008

	waitObject0 = 0x0000
	waitTimeout = 0x0102
)

// inputRecord mirrors INPUT_RECORD: WORD EventType, two bytes of union
// alignment, then the 16-byte event union. WINDOW_BUFFER_SIZE_EVENT's
// union holds a COORD — the new buffer size, which the redraw re-probes
// anyway, so the record is consumed and discarded.
type inputRecord struct {
	eventType uint16
	_         uint16
	event     [8]uint16
}

func startResizeWatcher(notify func()) (stop func()) {
	h := syscall.Handle(os.Stdin.Fd())

	var stopped atomic.Bool
	go func() {
		var rec inputRecord
		var read uint32
		for !stopped.Load() {
			// WINDOW_BUFFER_SIZE_EVENT records only appear while
			// ENABLE_WINDOW_INPUT is set. Raw mode sets it, but an action
			// may have restored the cooked mode in between, so re-assert
			// the bit each cycle — cheap, and harmless when it is set.
			var mode uint32
			if r1, _, _ := procGetConsoleModeR.Call(uintptr(h), uintptr(unsafe.Pointer(&mode))); r1 != 0 && mode&enableWindowInputWin == 0 {
				procSetConsoleModeR.Call(uintptr(h), uintptr(mode|enableWindowInputWin))
			}
			// Wait for input of any kind, bounded — the stop flag is only
			// honored between cycles, so the wait must not block forever.
			if r1, _, _ := procWaitForObjectR.Call(uintptr(h), 500); r1 != waitObject0 {
				continue // timeout, or not a waitable handle — try again
			}
			for {
				r1, _, _ := procPeekConsoleInputR.Call(uintptr(h), uintptr(unsafe.Pointer(&rec)), 1, uintptr(unsafe.Pointer(&read)))
				if r1 == 0 || read == 0 {
					return // not a console input handle (mintty pipes, redirect) — done
				}
				if rec.eventType != windowBufferSizeEvent {
					break // someone else's record — leave it at the front
				}
				r1, _, _ = procReadConsoleInputR.Call(uintptr(h), uintptr(unsafe.Pointer(&rec)), 1, uintptr(unsafe.Pointer(&read)))
				if r1 == 0 {
					return
				}
				notify()
			}
		}
	}()
	return func() { stopped.Store(true) }
}
