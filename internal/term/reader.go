// Keystroke decoding for the interactive menu: Reader.ReadKey turns the raw
// bytes a terminal delivers into KeyMsg values, mirroring tui_loop in
// the bash original, which reads one char with `read -rsn1` and
// disambiguates ESC-prefixed sequences with `read -rsn2 -t 0.05`. A terminal
// sends a whole escape sequence (e.g. ESC [ A) in a single burst while a lone
// ESC keypress sends nothing more, and both the script's timeout and the
// escWindow select below rely on exactly that timing — so a real tty source
// gets precisely the bash behavior.

package term

import (
	"errors"
	"io"
	"os"
	"time"
)

type Key int

const (
	KeyRune Key = iota
	KeyEnter
	KeyEscape
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeySpace
	KeyCtrlD
	KeyCtrlC
	KeyOther
)

type KeyMsg struct {
	Type Key
	Rune rune
}

type KeySource interface {
	ReadByte() (byte, error)
}

var escWindow = 50 * time.Millisecond

const escByte = 0x1b

const maxCSIParams = 16

type readResult struct {
	b   byte
	err error
}

type Reader struct {
	src  KeySource
	peek chan readResult
}

func NewReader(src KeySource) *Reader {
	return &Reader{src: src}
}

func NewReaderStdin() *Reader {
	return NewReader(stdinSource{os.Stdin})
}

type stdinSource struct{ f *os.File }

func (s stdinSource) ReadByte() (byte, error) {
	var b [1]byte
	n, err := s.f.Read(b[:])
	if n == 1 {
		return b[0], nil
	}
	if err == nil {
		err = io.EOF
	}
	return 0, err
}

func (r *Reader) ReadKey() (KeyMsg, error) {
	b, err := r.next()
	if err != nil {
		return KeyMsg{}, err
	}
	if b == escByte {
		return r.readEscape()
	}
	return decodeByte(b), nil
}

func (r *Reader) next() (byte, error) {
	if r.peek != nil {
		res := <-r.peek
		r.peek = nil
		if res.err != nil {
			return 0, res.err
		}
		return res.b, nil
	}
	return r.src.ReadByte()
}

func decodeByte(b byte) KeyMsg {
	switch b {
	case '\r', '\n':
		return KeyMsg{Type: KeyEnter}
	case ' ':
		return KeyMsg{Type: KeySpace}
	case 0x03:
		return KeyMsg{Type: KeyCtrlC}
	case 0x04:
		return KeyMsg{Type: KeyCtrlD}
	default:
		return KeyMsg{Type: KeyRune, Rune: rune(b)}
	}
}

func (r *Reader) readEscape() (KeyMsg, error) {
	ch := make(chan readResult, 1)
	go func() {
		b, err := r.src.ReadByte()
		ch <- readResult{b, err}
	}()

	var nb byte
	select {
	case res := <-ch:
		if res.err != nil {
			if errors.Is(res.err, io.EOF) {
				return KeyMsg{Type: KeyEscape}, nil
			}
			return KeyMsg{}, res.err
		}
		nb = res.b
	case <-time.After(escWindow):
		r.peek = ch
		return KeyMsg{Type: KeyEscape}, nil
	}

	if nb != '[' {
		return KeyMsg{Type: KeyOther}, nil
	}
	return r.readCSI()
}

func (r *Reader) readCSI() (KeyMsg, error) {
	var params []byte
	for {
		b, err := r.next()
		if err != nil {
			return KeyMsg{}, err
		}
		if b >= 0x40 && b <= 0x7e {
			if key, ok := csiKey(params, b); ok {
				return KeyMsg{Type: key}, nil
			}
			return KeyMsg{Type: KeyOther}, nil
		}
		params = append(params, b)
		if len(params) > maxCSIParams {
			return KeyMsg{Type: KeyOther}, nil
		}
	}
}

func csiKey(params []byte, final byte) (key Key, ok bool) {
	if len(params) == 0 {
		switch final {
		case 'A':
			return KeyUp, true
		case 'B':
			return KeyDown, true
		case 'H':
			return KeyHome, true
		case 'F':
			return KeyEnd, true
		}
		return 0, false
	}
	if final == '~' {
		switch string(params) {
		case "1", "7":
			return KeyHome, true
		case "4", "8":
			return KeyEnd, true
		}
	}
	return 0, false
}
