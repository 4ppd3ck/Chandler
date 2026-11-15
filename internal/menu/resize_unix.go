// resize_unix.go: the SIGWINCH feed for the TUI's immediate redraw on
// terminal resize (unix: darwin, linux, the BSDs). The watcher stops
// with signal.Stop when the menu ends; its goroutine, parked on the
// channel, dies with the process.

//go:build unix

package menu

import (
	"os"
	"os/signal"
	"syscall"
)

func startResizeWatcher(notify func()) (stop func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGWINCH)
	go func() {
		for range c {
			notify()
		}
	}()
	return func() { signal.Stop(c) }
}
