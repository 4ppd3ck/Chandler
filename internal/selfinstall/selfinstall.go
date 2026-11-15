// Package selfinstall keeps the conventional `chandler` command —
// installTarget, ~/.local/bin/chandler — present and current: the
// startup keep-alive that classifies and applies an install or refresh,
// the atomic temp-file-and-rename EnsureCopy both the startup and the
// `update`/`install` subcommands share, and the checkout-binary lookup
// `update` refreshes from.
package selfinstall

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"chandler/internal/home"
	"chandler/internal/rootpin"
)

// ErrRefreshSkipped and ErrRefreshCurrent are EnsureCopy's non-failure
// outcomes: nothing to do (the installed file IS the source, or there is
// no usable source), and the installed copy already matches the source.
var (
	ErrRefreshSkipped = errors.New("self-refresh skipped")
	ErrRefreshCurrent = errors.New("installed copy already current")
)

// hasRegularFile reports whether path exists as a regular file (bash's
// [[ -f ... ]], following symlinks).
func hasRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// InstallTarget returns the conventional install target of the `chandler`
// command: ~/.local/bin/chandler (home.LocalBin), with the windows
// executable suffix so the installed command is directly runnable there.
func InstallTarget() string {
	return filepath.Join(home.LocalBin(), CommandName())
}

// CommandName is the `chandler` command's file name on this platform:
// `chandler` on unix, `chandler.exe` on windows.
func CommandName() string {
	if runtime.GOOS == "windows" {
		return "chandler.exe"
	}
	return "chandler"
}

// CheckoutBinary is the conventional name of the built binary inside the
// checkout's GO module — what `go build` (or build.sh) leaves
// there — with the windows executable suffix.
func CheckoutBinary() string {
	if runtime.GOOS == "windows" {
		return "chandler.exe"
	}
	return "chandler"
}

// CheckoutBinaryPath returns the conventional built binary inside the
// checkout at root — the file `update` refreshes the installed command
// from. The flat layout keeps it at the root; the nested one in GO/.
func CheckoutBinaryPath(root string) string {
	if flat := filepath.Join(root, CheckoutBinary()); hasRegularFile(flat) {
		return flat
	}
	return filepath.Join(root, "GO", CheckoutBinary())
}

// EnsureInstalledCopy best-effort ensures the conventional `chandler`
// command — InstallTarget, ~/.local/bin/chandler — matches source: the
// checkout binary at <root>/GO/chandler. Unlike the running
// file the old refresh targeted, the installed side need not exist: after
// `update` the canonical command is guaranteed present and current, whether
// that means installed (a deleted or never-installed `chandler` — the
// user-reported gap), replaced, or left alone. When the running file IS
// the target, the rename-over-self below still works; when the running
// file is the source itself, EnsureCopy skips. The target's directory is
// created first (0755), so a first-ever install on a fresh machine cannot
// fail for a missing ~/.local/bin. Every hard failure is the caller's
// warning, never the update's exit code.
func EnsureInstalledCopy(source string) error {
	target := InstallTarget()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return EnsureCopy(target, source)
}

// EnsureCopy makes installed match source's bytes: a differing installed
// file is replaced, a MISSING installed file is installed — the new case
// that turns a deleted `chandler` command into an install instead of an
// error — and matching bytes are reported as current. The new bytes land
// in a temp file beside installed, get the executable bits, and are
// renamed over installed — a rename is atomic and swaps the directory
// entry instead of clobbering a running binary in place (no ETXTBSY);
// rename-over-self, the running copy being the target, works the same
// way. Skips (ErrRefreshSkipped) when installed IS source or when source
// is missing or not a regular file; ErrRefreshCurrent when the bytes
// already match; a real error only when an existing installed file cannot
// be read (a directory, wrong permissions) or the replacement cannot be
// written — note the installed file's parent must exist, which
// EnsureInstalledCopy and the `install` subcommand guarantee.
func EnsureCopy(installed, source string) error {
	if installed == source {
		return ErrRefreshSkipped
	}
	if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
		return ErrRefreshSkipped
	}
	srcBytes, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	curBytes, err := os.ReadFile(installed)
	if err != nil {
		// A missing installed file is the install case, not a failure;
		// anything else — an existing but unreadable file, a directory —
		// still is.
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	} else if bytes.Equal(srcBytes, curBytes) {
		return ErrRefreshCurrent
	}
	tmp := installed + fmt.Sprintf(".tmp-%d", os.Getpid())
	if err := os.WriteFile(tmp, srcBytes, 0o755); err != nil {
		return err
	}
	// WriteFile's perm is subject to the umask; make the exec bit stick.
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, installed); err != nil {
		// On windows the one rename that fails is the important one here:
		// replacing a RUNNING executable's image in place is denied, and
		// installed may well be the running chandler.exe (the self-refresh
		// path). A running image may still be RENAMED, though — so move
		// the old file aside, put the new bytes on the target name, and
		// leave a stale .old-<pid> behind (the running image itself
		// cannot be deleted; CleanupStaleOld sweeps it after a relaunch).
		if runtime.GOOS == "windows" {
			old := installed + fmt.Sprintf(".old-%d", os.Getpid())
			if err2 := os.Rename(installed, old); err2 == nil {
				if err3 := os.Rename(tmp, installed); err3 == nil {
					_ = os.Remove(old)
					CleanupStaleOld(installed)
					return nil
				}
				os.Rename(old, installed) // rollback: old name back
			}
		}
		os.Remove(tmp)
		return err
	}
	CleanupStaleOld(installed)
	return nil
}

// CleanupStaleOld removes leftover .old-<pid> siblings of installed from
// earlier refreshes whose running image could not be deleted in place —
// after a relaunch they are ordinary dead files. Failures are ignored:
// a leftover .old file is cosmetic, never a functional problem.
func CleanupStaleOld(installed string) {
	matches, err := filepath.Glob(installed + ".old-*")
	if err != nil {
		return
	}
	for _, m := range matches {
		os.Remove(m)
	}
}

// StartupAction is what the interactive startup ensure decided to do
// about the installed `chandler` command.
type StartupAction int

const (
	StartupCurrent StartupAction = iota // running as the target, or the bytes already match: a silent no-op
	StartupRefresh                      // the target exists and differs: replace it
	StartupInstall                      // the target is missing: install it
)

// ClassifyCommandStartup decides what the interactive startup ensure must
// do for the installed command at target given the running binary at
// source: StartupCurrent — silently nothing — when target IS source (the
// launched copy is the installed command itself) or when both files exist
// and match byte-for-byte; StartupRefresh when an existing target
// differs; StartupInstall when the target is missing (a deleted or
// never-installed `chandler`). A target or source that cannot be read is an
// error, not a guess — the caller warns once and moves on. The common
// case costs two stat calls and zero output; the full byte comparison
// runs only when both files have the same size (sizes differing can only
// mean different content).
func ClassifyCommandStartup(target, source string) (StartupAction, error) {
	if target == source {
		return StartupCurrent, nil
	}
	tgtInfo, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return StartupInstall, nil
	}
	if err != nil {
		return 0, err
	}
	srcInfo, err := os.Stat(source)
	if err != nil {
		return 0, err
	}
	if srcInfo.Size() != tgtInfo.Size() || !srcInfo.Mode().IsRegular() {
		return StartupRefresh, nil
	}
	srcBytes, err := os.ReadFile(source)
	if err != nil {
		return 0, err
	}
	dstBytes, err := os.ReadFile(target)
	if err != nil {
		return 0, err
	}
	if bytes.Equal(srcBytes, dstBytes) {
		return StartupCurrent, nil
	}
	return StartupRefresh, nil
}

// ApplyStartupEnsure performs the action ClassifyCommandStartup chose:
// silent for StartupCurrent; a refresh or install through EnsureCopy —
// the same atomic temp-file-and-rename the subcommands use — announced
// with one line before and one after. Failures are warned on errW and
// swallowed: a broken ensure must never cost the interactive session its
// exit code. The target's directory is created only when something will
// actually be installed into it.
func ApplyStartupEnsure(out, errW io.Writer, target, source string, action StartupAction) {
	switch action {
	case StartupCurrent:
		// The common case: the command is the running binary itself, or an
		// identical copy of it. Zero output, zero writes.
	case StartupRefresh:
		fmt.Fprintf(out, "chandler: updating installed command at %s...\n", target)
		if err := EnsureCopy(target, source); err != nil {
			fmt.Fprintf(errW, "chandler: refresh failed: %v\n", err)
			return
		}
		fmt.Fprintf(out, "chandler: refreshed from %s\n", source)
	case StartupInstall:
		fmt.Fprintf(out, "chandler: installing command to %s...\n", target)
		// A first-ever install on a fresh machine has no ~/.local/bin yet;
		// EnsureCopy needs the target's parent to exist.
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fmt.Fprintf(errW, "chandler: install failed: %v\n", err)
			return
		}
		if err := EnsureCopy(target, source); err != nil {
			fmt.Fprintf(errW, "chandler: install failed: %v\n", err)
			return
		}
		fmt.Fprintf(out, "chandler: installed to %s\n", target)
	}
}

// EnsureCommandOnStartup is the interactive path's best-effort keep-alive
// for the `chandler` command: it classifies InstallTarget (~/.local/bin/
// chandler) against the running executable VERBATIM
// — not symlink-resolved, so launching through a symlink to the checkout
// binary counts as current — and applies the result. Nothing is printed
// when the command is already current, one line pair when it is installed
// or refreshed, one warning line when the check itself fails; it returns
// nothing and never exits, so the menu always comes up. Only the
// interactive flow calls this — the `update` and `install` subcommands
// keep their own ensure and messages.
func EnsureCommandOnStartup(out, errW io.Writer) {
	source, err := os.Executable() // verbatim, NOT symlink-resolved
	if err != nil {
		fmt.Fprintf(errW, "chandler: startup check skipped: %v\n", err)
		return
	}
	source = filepath.Clean(source)
	target := InstallTarget()
	action, err := ClassifyCommandStartup(target, source)
	if err != nil {
		fmt.Fprintf(errW, "chandler: startup check failed: %v\n", err)
		return
	}
	ApplyStartupEnsure(out, errW, target, source, action)
}

// RootPinDir is the directory the root-pin file lives in — exported
// because uninstall removes the whole directory (its only resident is
// the pin file).
func RootPinDir() string {
	return filepath.Dir(rootpin.RootPinPath())
}
