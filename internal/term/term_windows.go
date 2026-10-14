//go:build windows

package term

import (
	"fmt"
	"syscall"
)

func isTTY(fd uintptr) bool {
	var mode uint32
	return getConsoleMode(syscall.Handle(fd), &mode) == nil
}

func termSize(fd uintptr) (cols, lines int, err error) {
	if info, ok := screenBufferInfo(syscall.Handle(fd)); ok {
		c, l := rectSize(info.window)
		return c, l, nil
	}
	h, err := openConsoleOutput()
	if err != nil {
		return defaultCols, defaultLines, fmt.Errorf("console size: %w", err)
	}
	defer syscall.CloseHandle(h)
	info, ok := screenBufferInfo(h)
	if !ok {
		return defaultCols, defaultLines, fmt.Errorf("console size: GetConsoleScreenBufferInfo failed")
	}
	c, l := rectSize(info.window)
	return c, l, nil
}

func rectSize(w smallRect) (cols, lines int) {
	return int(w.right-w.left) + 1, int(w.bottom-w.top) + 1
}

func init() {
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return
	}
	var mode uint32
	if getConsoleMode(h, &mode) != nil {
		return
	}
	_ = setConsoleMode(h, mode|enableVirtualTerminalProcessing)
}
