// Raw mode for the interactive menu: MakeRaw switches a tty to the
// byte-at-a-time, no-echo state that bash gets per-read from the script's
// `read -rsn1` (the -s silences echo and no line editing happens because
// bash reads one char at a time), and which Reader.ReadKey needs when its
// source is a real terminal. Restore puts the original settings back, the
// way the script's EXIT trap hands back a sane terminal.
//
// The console state lives per OS: termios ioctls on darwin and linux
// (raw_bsd.go, raw_linux.go), the Windows console input mode on
// windows (raw_windows.go), and a clear unsupported error everywhere
// else (raw_other.go). termiosState is the per-OS alias for what MakeRaw
// saves and Restore puts back.

package term

import "errors"

type State struct {
	fd    uintptr
	saved termiosState
	valid bool
}

func MakeRaw(fd uintptr) (*State, error) {
	old, err := makeRaw(fd)
	if err != nil {
		return nil, err
	}
	return &State{fd: fd, saved: old, valid: true}, nil
}

func Restore(s *State) error {
	if s == nil || !s.valid {
		return errors.New("term: Restore of a State that MakeRaw did not return")
	}
	return restoreTermios(s.fd, &s.saved)
}
