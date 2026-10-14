//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package term

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	tiocgeta = 0x40487413
	tiocseta = 0x80487414
)

type termiosState = syscall.Termios

func makeRaw(fd uintptr) (syscall.Termios, error) {
	var old syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, tiocgeta, uintptr(unsafe.Pointer(&old)))
	if errno != 0 {
		return syscall.Termios{}, fmt.Errorf("TIOCGETA: %w", errno)
	}

	raw := old
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG | syscall.IEXTEN
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, tiocseta, uintptr(unsafe.Pointer(&raw)))
	if errno != 0 {
		return syscall.Termios{}, fmt.Errorf("TIOCSETA: %w", errno)
	}
	return old, nil
}

func restoreTermios(fd uintptr, saved *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, tiocseta, uintptr(unsafe.Pointer(saved)))
	if errno != 0 {
		return fmt.Errorf("TIOCSETA: %w", errno)
	}
	return nil
}
