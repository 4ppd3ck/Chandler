//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package term

import (
	"fmt"
	"syscall"
	"unsafe"
)

type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

func isTTY(fd uintptr) bool {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, tiocgwinsz, uintptr(unsafe.Pointer(&ws)))
	return errno == 0
}

func termSize(fd uintptr) (cols, lines int, err error) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, tiocgwinsz, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return defaultCols, defaultLines, fmt.Errorf("TIOCGWINSZ: %w", errno)
	}
	return int(ws.cols), int(ws.rows), nil
}
