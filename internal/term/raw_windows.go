//go:build windows

package term

import (
	"fmt"
	"syscall"
)

type termiosState = uint32

func makeRaw(fd uintptr) (termiosState, error) {
	h := syscall.Handle(fd)
	var old uint32
	if err := getConsoleMode(h, &old); err != nil {
		return 0, fmt.Errorf("GetConsoleMode: %w", err)
	}

	raw := (old | enableVirtualTerminalInput | enableExtendedFlags | enableWindowInput) &^
		(enableLineInput | enableEchoInput | enableProcessedInput |
			enableMouseInput | enableInsertMode | enableQuickEditMode)

	if err := setConsoleMode(h, raw); err != nil {
		return 0, fmt.Errorf("SetConsoleMode: %w", err)
	}
	return old, nil
}

func restoreTermios(fd uintptr, saved *termiosState) error {
	if err := setConsoleMode(syscall.Handle(fd), *saved); err != nil {
		return fmt.Errorf("SetConsoleMode: %w", err)
	}
	return nil
}
