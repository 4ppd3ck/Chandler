//go:build !darwin && !linux && !windows && !freebsd && !netbsd && !openbsd && !dragonfly

package term

import (
	"errors"
	"syscall"
)

type termiosState = syscall.Termios

var errRawUnsupported = errors.New("term: raw mode is not supported on this platform")

func makeRaw(fd uintptr) (termiosState, error) {
	return termiosState{}, errRawUnsupported
}

func restoreTermios(fd uintptr, saved *termiosState) error {
	return errRawUnsupported
}
