// Package pathsetup ensures ~/.local/bin is on the PATH, the way the
// bash original's "Ensure $LOCAL_BIN is on PATH" block did after the
// notes and before the menu. The config file differs per platform; the
// appended line always sits inside a chandler marker block, so both the
// write (idempotent) and the uninstall strip stay one exact match.
package pathsetup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"chandler/internal/home"
)

// ErrNoShellConfig is returned by EnsureLocalBinOnPath when neither
// ~/.bashrc nor ~/.zshrc exists and no platform rule creates one.
var ErrNoShellConfig = errors.New("no compatible shell config file found")

// The marker lines chandler wraps everything it appends to a shell config
// in. The block is the unit install writes (idempotently — a config that
// already carries one is left alone) and uninstall strips, so both
// directions stay one exact match instead of a heuristic.
const (
	markerBegin = "# >>> chandler >>>"
	markerEnd   = "# <<< chandler <<<"
)

// hasChandlerBlock reports whether s already carries a chandler marker
// block (both marker lines present — the assumption install and uninstall
// share).
func hasChandlerBlock(s string) bool {
	return strings.Contains(s, markerBegin) && strings.Contains(s, markerEnd)
}

// hasRegularFile reports whether path exists as a regular file (bash's
// [[ -f ... ]], following symlinks).
func hasRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// IsZshLoginShell reports whether the login shell ($SHELL) is zsh — the
// heuristic that lets a fresh zsh-only machine bootstrap a ~/.zshrc
// instead of failing with no shell config at all.
func IsZshLoginShell() bool {
	return filepath.Base(os.Getenv("SHELL")) == "zsh"
}

// FileHasChandlerBlock reports whether configFile is readable and carries
// a chandler marker block. (Exported for the doctor's shell-config
// report.)
func FileHasChandlerBlock(configFile string) bool {
	data, err := os.ReadFile(configFile)
	return err == nil && hasChandlerBlock(string(data))
}

// EnsureLocalBinOnPath ensures LOCAL_BIN is on the PATH. LOCAL_BIN is
// ~/.local/bin under the user's home (home.LocalBin — $HOME, with
// %USERPROFILE% as the windows fallback).
//
// The config file differs per platform:
//
//   - every platform appends the bash export line — inside a chandler
//     marker block — to every existing config among ~/.bashrc and
//     ~/.zshrc (zsh reads only the latter; Git Bash on windows reads
//     .bashrc too);
//   - unix with a zsh login shell and neither file: ~/.zshrc is created
//     with the block — a fresh macOS machine (zsh is the default since
//     Catalina) usually ships without one, and failing here would leave
//     the PATH never set up;
//   - windows, when neither exists: .bashrc is created (plus the
//     .bash_profile chain Git Bash sources for login shells) and both
//     PowerShell profiles get a $env:Path line — PowerShell never reads
//     .bashrc, and the bash script's hard exit on a missing dotfile would
//     leave a fresh Windows machine with nothing set up. cmd.exe reads
//     neither file and gets a pointer to the system settings instead;
//   - unix otherwise, when neither exists: the bash script's behavior —
//     report and fail (ErrNoShellConfig).
//
// Every write is guarded so running the program twice in a shell that has
// not re-sourced its config cannot pile up duplicate lines.
func EnsureLocalBinOnPath() error {
	localBin := home.LocalBin()
	sep := string(os.PathListSeparator) // ":" on unix, ";" on windows
	configs := shellConfigs()

	// The current PATH alone cannot skip the write: after an uninstall the
	// running shell still carries the old PATH, but its config no longer
	// holds the block — a fresh terminal would lose the command. Skip only
	// when every existing config already carries the block (or none exists
	// to hold one) and the directory is on the current PATH.
	blocked := len(configs) > 0
	for _, c := range configs {
		if !FileHasChandlerBlock(c) {
			blocked = false
			break
		}
	}
	if blocked && strings.Contains(sep+os.Getenv("PATH")+sep, sep+localBin+sep) {
		fmt.Printf("%s is already in the PATH.\n", localBin)
		return nil
	}

	fmt.Printf("%s is not in the PATH. Adding it...\n", localBin)

	if len(configs) > 0 {
		for _, c := range configs {
			if err := AppendPathLineToShellConfig(c, localBin); err != nil {
				return err
			}
		}
	} else if runtime.GOOS == "windows" {
		if err := createWindowsBashConfig(localBin); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not set up a bash config: %v\n", err)
		}
	} else if IsZshLoginShell() {
		// A zsh login shell with no shell config at all: create ~/.zshrc
		// with the block instead of failing — a fresh macOS machine (zsh is
		// the default login shell since Catalina) usually ships without
		// one, and a hard exit here would leave the PATH never set up.
		if err := AppendPathLineToShellConfig(filepath.Join(home.Dir(), ".zshrc"), localBin); err != nil {
			return err
		}
	} else {
		fmt.Println("No compatible shell config file found.")
		return ErrNoShellConfig
	}

	// Windows also hands the PATH line to the PowerShell profiles; both
	// flavors get it, best-effort — a failure here never stops the menu.
	if runtime.GOOS == "windows" {
		if err := ensurePowerShellProfiles(localBin); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update a PowerShell profile: %v\n", err)
		}
		fmt.Println("Note: cmd.exe reads none of these files — add the folder to your PATH via System Properties → Environment Variables.")
	}

	// bash re-sources the config file here; the Go port just prepends
	// LOCAL_BIN to the current process PATH so child installers inherit
	// it without re-executing the user's shell config. The separator is
	// the platform's (see sep above), not the bash syntax just written.
	os.Setenv("PATH", localBin+sep+os.Getenv("PATH"))
	fmt.Printf("%s has been added to the PATH.\n", localBin)
	return nil
}

// shellConfigs lists the existing shell configs the PATH block belongs
// in: ~/.bashrc and ~/.zshrc, whichever exist. Every existing one gets
// the block, not just the first — a zsh user reads the command's PATH
// from .zshrc (which zsh, not bash, sources), and one whose home carries
// both files uses both shells.
func shellConfigs() []string {
	var out []string
	for _, name := range []string{".bashrc", ".zshrc"} {
		if candidate := filepath.Join(home.Dir(), name); hasRegularFile(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

// AppendPathLineToShellConfig appends the bash PATH export for localBin
// to configFile inside a chandler marker block, creating the file when
// missing, and skips the append when the file already carries a block
// (see EnsureLocalBinOnPath). The written lines stay bash syntax on every
// platform, because the config is a bash (or zsh) file. The block is what
// uninstall strips, so it must be the only thing this write adds.
func AppendPathLineToShellConfig(configFile, localBin string) error {
	if FileHasChandlerBlock(configFile) {
		return nil
	}
	// A config whose last line lacks a trailing newline would glue the
	// begin marker onto that line — the marker would then stop matching
	// line-exactly and uninstall's strip would never remove the block —
	// so start on a fresh line in that case.
	needsNewline := false
	if data, err := os.ReadFile(configFile); err == nil && len(data) > 0 && data[len(data)-1] != '\n' {
		needsNewline = true
	}
	f, err := os.OpenFile(configFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	// echo "export PATH=\"$LOCAL_BIN:\$PATH\"" >> "$CONFIG_FILE" — the
	// real path interpolated, the trailing $PATH literal.
	prefix := ""
	if needsNewline {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + fmt.Sprintf("%s\nexport PATH=\"%s:$PATH\"\n%s\n", markerBegin, localBin, markerEnd))
	return err
}

// createWindowsBashConfig boots a Git Bash setup when the user has no
// shell config at all: a ~/.bashrc carrying the PATH block, plus a
// ~/.bash_profile that sources it — a login bash (what Git Bash runs)
// reads .bash_profile, not .bashrc directly, and Git Bash's own first-run
// profile chains the two exactly like this. The chain line lands in its
// own marker block: we only ever write it into a profile we created, and
// uninstall strips what it recognizes.
func createWindowsBashConfig(localBin string) error {
	if err := AppendPathLineToShellConfig(filepath.Join(home.Dir(), ".bashrc"), localBin); err != nil {
		return err
	}
	profile := filepath.Join(home.Dir(), ".bash_profile")
	if hasRegularFile(profile) {
		return nil
	}
	chain := fmt.Sprintf("%s\n[ -f ~/.bashrc ] && . ~/.bashrc\n%s\n", markerBegin, markerEnd)
	return os.WriteFile(profile, []byte(chain), 0o644)
}

// ensurePowerShellProfiles adds the PATH line — inside a chandler marker
// block — to each PowerShell flavor's profile: Documents\PowerShell\ for
// PowerShell 7+ and Documents\WindowsPowerShell\ for Windows PowerShell
// 5.1. Both are created when missing (a flavor without a profile starts
// loading the line) and left alone when they already carry a block.
// Documents is resolved under the home directory — a OneDrive-redirected
// Documents is missed, which costs the line, never anything else.
func ensurePowerShellProfiles(localBin string) error {
	docs := filepath.Join(home.Dir(), "Documents")
	var firstErr error
	for _, dir := range []string{
		filepath.Join(docs, "PowerShell"),
		filepath.Join(docs, "WindowsPowerShell"),
	} {
		path := filepath.Join(dir, "Microsoft.PowerShell_profile.ps1")
		if FileHasChandlerBlock(path) {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		_, werr := f.WriteString(fmt.Sprintf("%s\n$env:Path = \"%s;$env:Path\"\n%s\n", markerBegin, localBin, markerEnd))
		f.Close()
		if werr != nil && firstErr == nil {
			firstErr = werr
		}
	}
	return firstErr
}
