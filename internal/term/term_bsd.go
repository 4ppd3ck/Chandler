//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package term

const tiocgwinsz = 0x40087468
