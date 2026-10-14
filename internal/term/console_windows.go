//go:build windows

// Console API plumbing shared by raw_windows.go (input mode) and
// term_windows.go (tty probe, window size, VT output). Everything goes
// through syscall.NewLazyDLL because the stdlib syscall package exposes
// none of these entry points; the handles arrive as os.Stdin/os.Stdout
// fds, which on windows are the underlying console HANDLE values.

package term

import (
	"fmt"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")

var (
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	enableProcessedInput       = 0x0001
	enableLineInput            = 0x0002
	enableEchoInput            = 0x0004
	enableWindowInput          = 0x0008
	enableMouseInput           = 0x0010
	enableInsertMode           = 0x0020
	enableQuickEditMode        = 0x0040
	enableExtendedFlags        = 0x0080
	enableVirtualTerminalInput = 0x0200
)

const enableVirtualTerminalProcessing = 0x0004

type consoleScreenBufferInfo struct {
	size              coord
	cursorPosition    coord
	attributes        uint16
	window            smallRect
	maximumWindowSize coord
}

type (
	coord     struct{ x, y int16 }
	smallRect struct{ left, top, right, bottom int16 }
)

func getConsoleMode(h syscall.Handle, mode *uint32) error {
	r1, _, callErr := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(mode)))
	if r1 == 0 {
		return fmt.Errorf("GetConsoleMode: %w", callErr)
	}
	return nil
}

func setConsoleMode(h syscall.Handle, mode uint32) error {
	r1, _, callErr := procSetConsoleMode.Call(uintptr(h), uintptr(mode))
	if r1 == 0 {
		return fmt.Errorf("SetConsoleMode: %w", callErr)
	}
	return nil
}

func screenBufferInfo(h syscall.Handle) (info consoleScreenBufferInfo, ok bool) {
	r1, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	return info, r1 != 0
}

func openConsoleOutput() (syscall.Handle, error) {
	path, err := syscall.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return 0, err
	}
	h, err := syscall.CreateFile(path,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return 0, err
	}
	return h, nil
}
