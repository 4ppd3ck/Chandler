// Package pathsetup ensures ~/.local/bin is on the PATH, the way the
// bash original's "Ensure $LOCAL_BIN on PATH" block did after the
// notes and before the menu. The config file differs per platform; the
// appended line always sits inside a chandler marker block, so both the
// write (idempotent) and the uninstall strip stay one exact match.
//
// Shell coverage, per platform:
//
//   - bash / zsh: the export line in every existing ~/.bashrc and
//     ~/.zshrc; a fresh zsh-only machine gets ~/.zshrc created, a fresh
//     bash-only machine gets ~/.bashrc plus the ~/.bash_profile chain
//     (see createBashConfig);
//   - a login bash whose existing ~/.bash_profile never sources
//     ~/.bashrc gets the chain appended, so the block reaches login
//     shells (ensureBashProfileChain);
//   - fish: a fish_add_path line in ~/.config/fish/config.fish when the
//     machine shows fish at all (ensureFishConfig) — fish reads neither
//     bash nor zsh configs;
//   - windows: all of the above through Git Bash's configs, both
//     PowerShell profiles (resolved through the real Documents folder,
//     OneDrive redirection included), and the user-scope registry PATH
//     for cmd.exe (ensureCmdUserPath) — cmd reads no rc files, and setx
//     is deliberately avoided for its 1024-char truncation.
package pathsetup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"chandler/internal/home"
)

// ErrNoShellConfig is returned by EnsureLocalBinOnPath when no shell of
// the covered families could be wired — no ~/.bashrc, no ~/.zshrc, a
// login shell that is neither bash nor zsh, and no fish either.
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

// LoginShellName returns the base name of the login shell ($SHELL) —
// "unknown" when unset. Exported for the doctor's report.
func LoginShellName() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "unknown"
	}
	return filepath.Base(shell)
}

// IsZshLoginShell reports whether the login shell ($SHELL) is zsh — the
// heuristic that lets a fresh zsh-only machine bootstrap a ~/.zshrc
// instead of failing with no shell config at all.
func IsZshLoginShell() bool {
	return LoginShellName() == "zsh"
}

// IsBashLoginShell reports whether the login shell ($SHELL) is bash — the
// bootstrap twin of IsZshLoginShell.
func IsBashLoginShell() bool {
	return LoginShellName() == "bash"
}

// IsFishLoginShell reports whether the login shell ($SHELL) is fish.
func IsFishLoginShell() bool {
	return LoginShellName() == "fish"
}

// FishConfigPath is the config file fish reads at startup on every
// platform (~/.config/fish/config.fish). Exported for the doctor's
// report and the uninstall strip.
func FishConfigPath() string {
	return filepath.Join(home.Dir(), ".config", "fish", "config.fish")
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
// The bash/zsh config file differs per platform:
//
//   - every platform appends the bash export line — inside a chandler
//     marker block — to every existing config among ~/.bashrc and
//     ~/.zshrc (zsh reads only the latter; Git Bash on windows reads
//     .bashrc too);
//   - unix with a zsh login shell and neither file: ~/.zshrc is created
//     with the block — a fresh macOS machine (zsh is the default since
//     Catalina) usually ships without one, and failing here would leave
//     the PATH never set up;
//   - unix with a bash login shell and neither file: ~/.bashrc plus the
//     ~/.bash_profile chain are created, mirroring the zsh bootstrap;
//   - windows, when neither exists: .bashrc is created (plus the
//     .bash_profile chain Git Bash sources for login shells) and both
//     PowerShell profiles get a $env:Path line — PowerShell never reads
//     .bashrc, and the bash script's hard exit on a missing dotfile would
//     leave a fresh Windows machine with nothing set up. cmd.exe reads
//     neither file and gets the user-scope registry PATH instead;
//   - unix otherwise, when neither exists and fish could not be wired:
//     the bash script's behavior — report and fail (ErrNoShellConfig).
//
// fish is wired independently of the bash/zsh configs and before the
// skip check below, so a fish install that arrives after a bash one (or
// a machine whose bash side was already done) still gets its block —
// best-effort and silent when the machine shows no fish at all.
//
// Every write is guarded so running the program twice in a shell that has
// not re-sourced its config cannot pile up duplicate lines.
func EnsureLocalBinOnPath() error {
	localBin := home.LocalBin()
	sep := string(os.PathListSeparator) // ":" on unix, ";" on windows
	configs := shellConfigs()

	// fish first: fish_add_path -g into ~/.config/fish/config.fish when
	// the machine shows fish (its config directory or a fish login
	// shell). Silent no-op otherwise; a failure is a warning, never a
	// reason to give up on the other shells.
	fishWired, err := ensureFishConfig(localBin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not wire up the fish config: %v\n", err)
	}

	// The current PATH alone cannot skip the write: after an uninstall the
	// running shell still carries the old PATH, but its config no longer
	// holds the block — a fresh terminal would lose the command. Skip only
	// when every existing config already carries the block (or none exists
	// to hold one) and the directory is on the current PATH. (A fish or
	// PowerShell config added after a completed bash setup is caught by
	// the writes above running first, not by this check.)
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

	noConfig := false
	switch {
	case len(configs) > 0:
		for _, c := range configs {
			if err := AppendPathLineToShellConfig(c, localBin); err != nil {
				return err
			}
		}
	case runtime.GOOS == "windows":
		if err := createBashConfig(localBin); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not set up a bash config: %v\n", err)
		}
	case IsZshLoginShell():
		// A zsh login shell with no shell config at all: create ~/.zshrc
		// with the block instead of failing — a fresh macOS machine (zsh is
		// the default login shell since Catalina) usually ships without
		// one, and a hard exit here would leave the PATH never set up.
		if err := AppendPathLineToShellConfig(filepath.Join(home.Dir(), ".zshrc"), localBin); err != nil {
			return err
		}
	case IsBashLoginShell():
		// The bash twin of the zsh bootstrap: a fresh bash-only machine
		// gets ~/.bashrc plus the ~/.bash_profile chain, exactly what the
		// windows fresh-machine path writes for Git Bash.
		if err := createBashConfig(localBin); err != nil {
			return err
		}
	default:
		// Nothing bash/zsh-shaped to hold a block — fish may still have
		// saved the run above.
		noConfig = true
	}

	// A login bash reads ~/.bash_profile, not ~/.bashrc — an existing
	// profile that never sources .bashrc would keep the fresh block away
	// from login shells. Chain it (all platforms; the windows fresh
	// machine writes its own chain in createBashConfig, which this then
	// recognizes and skips). Errors are warnings: the block is in place
	// for interactive shells regardless.
	if err := ensureBashProfileChain(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not add the .bash_profile chain: %v\n", err)
	}

	if noConfig && !fishWired {
		fmt.Println("No compatible shell config file found.")
		return ErrNoShellConfig
	}

	// Windows also hands the PATH line to the PowerShell profiles and the
	// user-scope registry (cmd.exe); both are best-effort — a failure here
	// never stops the menu.
	if runtime.GOOS == "windows" {
		if err := ensurePowerShellProfiles(localBin); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update a PowerShell profile: %v\n", err)
		}
		added, err := ensureCmdUserPath(localBin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update the user PATH for cmd.exe: %v\n", err)
			fmt.Println("Note: cmd.exe reads none of these files — add the folder to your PATH via System Properties → Environment Variables.")
		} else if added {
			fmt.Printf("Added %s to the user PATH (registry) — cmd.exe picks it up in newly opened windows.\n", localBin)
		} else {
			fmt.Println("The user PATH (registry) already carries the folder for cmd.exe.")
		}
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

// appendMarkerBlock appends content to configFile wrapped in the chandler
// marker block, creating the file when missing, and skips the append when
// the file already carries any chandler block. The shared writer behind
// every block kind of this package — the bash/zsh export line, the fish
// line, the .bash_profile chain — so the guard and the newline handling
// stay one code path.
func appendMarkerBlock(configFile, content string) error {
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
	prefix := ""
	if needsNewline {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + markerBegin + "\n" + content + "\n" + markerEnd + "\n")
	return err
}

// AppendPathLineToShellConfig appends the bash PATH export for localBin
// to configFile inside a chandler marker block, creating the file when
// missing, and skips the append when the file already carries a block
// (see EnsureLocalBinOnPath). The written lines stay bash syntax on every
// platform, because the config is a bash (or zsh) file. The block is what
// uninstall strips, so it must be the only thing this write adds.
func AppendPathLineToShellConfig(configFile, localBin string) error {
	// echo "export PATH=\"$LOCAL_BIN:\$PATH\"" >> "$CONFIG_FILE" — the
	// real path interpolated, the trailing $PATH literal.
	return appendMarkerBlock(configFile, fmt.Sprintf(`export PATH="%s:$PATH"`, localBin))
}

// ensureBashProfileChain makes the PATH block reach login bash shells: a
// login bash reads ~/.bash_profile, which does not source ~/.bashrc
// unless the user wired it, so an existing profile without any .bashrc
// mention gets the chain line appended in its own marker block. Never
// creates .bash_profile on unix — bash would then read it INSTEAD of
// ~/.profile, cutting the user's own setup off — and never touches a
// profile that already chains .bashrc in some form (anything mentioning
// it counts). A no-op when ~/.bashrc carries no chandler block (nothing
// of ours to expose) or the profile is absent.
func ensureBashProfileChain() error {
	bashrc := filepath.Join(home.Dir(), ".bashrc")
	if !hasRegularFile(bashrc) || !FileHasChandlerBlock(bashrc) {
		return nil
	}
	profile := filepath.Join(home.Dir(), ".bash_profile")
	if !hasRegularFile(profile) {
		return nil
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), ".bashrc") {
		return nil
	}
	return appendMarkerBlock(profile, "[ -f ~/.bashrc ] && . ~/.bashrc")
}

// ensureFishConfig wires fish when the machine shows it at all: a fish
// login shell, a ~/.config/fish directory (an installed fish creates it
// even before its first config is written), or an existing config.fish.
// The block holds one fish_add_path -g line — global scope, prepended,
// deduplicated by fish itself, and nothing persisted outside config.fish,
// so uninstalling by strip reverts the whole effect (needs fish ≥ 3.2,
// when fish_add_path was added). Missing directories and files are
// created; the write is the shared guarded append. Reports whether fish
// was wired — a machine without any fish sign returns (false, nil).
func ensureFishConfig(localBin string) (bool, error) {
	confDir := filepath.Join(home.Dir(), ".config", "fish")
	confFile := FishConfigPath()
	if !IsFishLoginShell() && !hasRegularFile(confFile) {
		if info, err := os.Stat(confDir); err != nil || !info.IsDir() {
			return false, nil
		}
	}
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return true, err
	}
	// fish_add_path -g "$dir": global scope so the line in config.fish is
	// the single source of truth (universal scope would persist the entry
	// even after this block is stripped), prepend to match the bash
	// export's position.
	if err := appendMarkerBlock(confFile, fmt.Sprintf("fish_add_path -g %s", localBin)); err != nil {
		return true, err
	}
	return true, nil
}

// createBashConfig boots a bash setup when the user has no shell config
// at all: a ~/.bashrc carrying the PATH block, plus a ~/.bash_profile
// that sources it — a login bash (what Git Bash runs, and what a bash
// login shell runs anywhere) reads .bash_profile, not .bashrc directly,
// and Git Bash's own first-run profile chains the two exactly like this.
// The chain line lands in its own marker block: we only ever write it
// into a profile we created, and uninstall strips what it recognizes.
func createBashConfig(localBin string) error {
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
// Documents resolves through home.DocumentsDir, so a OneDrive-redirected
// Documents — the folder PowerShell actually reads its profiles from —
// is followed, not missed.
func ensurePowerShellProfiles(localBin string) error {
	docs := home.DocumentsDir()
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

// ensureCmdUserPath adds LOCAL_BIN to the user-scope PATH — the
// HKCU\Environment\Path registry value cmd.exe reads for new windows —
// by running the script below through powershell.exe (always present on
// windows; -NoProfile keeps the user's profile out of it). The folder is
// passed through an environment variable, so no path ever needs quoting
// or escaping inside the script. setx is deliberately avoided: it
// truncates PATH at 1024 characters and merges the machine scope into
// the user's view. SetEnvironmentVariable('…', 'User') writes the
// registry and broadcasts WM_SETTINGCHANGE, so newly opened windows (a
// fresh cmd from the Start menu, a new Terminal) see the folder;
// already-open shells keep their inherited PATH until restarted.
// Reports whether the entry was added.
func ensureCmdUserPath(localBin string) (bool, error) {
	return RunUserPathScript(cmdUserPathEnsureScript, localBin)
}

// cmdUserPathEnsureScript adds the folder to the user PATH when no entry
// matches it (trimmed, case-insensitive — the registry comparison cmd
// itself performs), and reports added/present.
const cmdUserPathEnsureScript = `
$bin = $env:CHANDLER_LOCAL_BIN
if ([string]::IsNullOrEmpty($bin)) { exit 1 }
$p = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($null -eq $p) { $p = '' }
foreach ($e in $p -split ';') {
    if ($e.Trim() -ieq $bin) { Write-Output 'present'; exit 0 }
}
$new = $p.TrimEnd(';')
if ($new -eq '') { $new = $bin } else { $new = $new + ';' + $bin }
[Environment]::SetEnvironmentVariable('Path', $new, 'User')
Write-Output 'added'
`

// RunUserPathScript runs one of the user-PATH PowerShell scripts (the
// ensure one here, the remove one in the uninstall package), feeding the
// folder through CHANDLER_LOCAL_BIN, and classifies the output:
// added=true only on the literal 'added' line, present/absent/removed on
// the others, and anything else is an error (with the script's output
// attached, so a PowerShell failure is diagnosable).
func RunUserPathScript(script, localBin string) (bool, error) {
	ps, err := exec.LookPath("powershell")
	if err != nil {
		return false, err
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "CHANDLER_LOCAL_BIN="+localBin)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	switch {
	case err != nil:
		return false, fmt.Errorf("powershell failed: %w (%s)", err, output)
	case output == "added":
		return true, nil
	case output == "present" || output == "absent" || output == "removed":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected powershell output: %s", output)
	}
}
