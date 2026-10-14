// Command chandler is the Go port of the repo's bash
// entrypoint (the bash original): a self-contained deployment tool
// that resolves the deployment root, checks its prerequisites, asks the
// stack/cloud questions into config.env, and offers a full-screen TUI
// (a plain numeric menu on non-terminals) whose entries install the
// k8s/cloud tool set via bash installers. The history of the port lives
// in CHANGELOG.md; the platform support matrix lives in README.md.
//
// Layout: main.go (startup, root resolution, self-refresh, subcommands),
// assets/ (embedded installers, dotfiles, custom scripts), internal/term
// (per-OS console: raw mode, tty probe, window size, keystroke decoding),
// internal/menu (TUI + action runner + bash resolution), internal/ask
// (stack/cloud pickers), internal/config (config.env), internal/pkgmgr
// (per-OS package managers), internal/customsync (helper-script sync),
// internal/home (the user's home on every platform).

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"chandler/assets"
	"chandler/internal/ask"
	"chandler/internal/claim"
	"chandler/internal/config"
	"chandler/internal/home"
	"chandler/internal/menu"
	"chandler/internal/pkgmgr"
	"chandler/internal/term"
)

// version is stamped at release build time via -ldflags
// -X main.version=<v> (scripts/release.sh); a plain `go build` reports dev.
var version = "dev"

// exeDir returns the directory of the running executable, with symlinks
// resolved (os.Executable + filepath.EvalSymlinks + filepath.Dir), or ""
// when the executable's path cannot be determined.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	return filepath.Dir(resolved)
}

// exeSelf returns the running executable's path verbatim — os.Executable
// only, deliberately NOT symlink-resolved — for the CHANDLER_SELF
// environment handed to the child installers: install-bashtools.sh copies
// that exact file onto the PATH as `chandler`, and it must be the real copy
// that is running, not whatever a symlink on the way points at. Empty
// when the path cannot be determined (the env var is then not exported).
func exeSelf() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// cwd returns the process's working directory, or "" when it cannot be
// determined (the chain then skips the cwd probes).
func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// rootEvidence identifies which candidate of the root chain produced the
// deployment root. resolveRoot persists only the marker-validated
// filesystem candidates — the exe-dir and cwd ones — to the pin file.
type rootEvidence int

const (
	evidenceEnv          rootEvidence = iota // CHANDLER_ROOT, verbatim
	evidenceExeDir                           // marker beside the executable
	evidenceExeDirParent                     // marker one level above it
	evidencePin                              // the root-pin file's value
	evidenceCwd                              // marker in the working directory
	evidenceCwdParent                        // marker one level above it
	evidenceDefault                          // $HOME/chandler fallback
)

// EnvRootPin overrides where the root-pin file lives; the file itself holds
// one absolute path — the deployment root an installed `chandler` copy
// resolves when nothing beside the binary or the cwd matches.
const EnvRootPin = "CHANDLER_ROOT_PIN"

// rootPinPath returns the root-pin file's location:
// $CHANDLER_ROOT_PIN when set, else
// ~/.config/chandler/path under the user's home (home.Dir).
func rootPinPath() string {
	if p := os.Getenv(EnvRootPin); p != "" {
		return p
	}
	return filepath.Join(home.Dir(), ".config", "chandler", "path")
}

// readRootPin returns the pinned root: the pin file's content, trimmed,
// and only when it is an absolute path. Anything else — a missing file,
// a read error, empty or relative content — reads as no pin (""); the
// candidate then just falls through the chain like any other
// non-matching one, so a stale pin never breaks resolution.
func readRootPin() string {
	data, err := os.ReadFile(rootPinPath())
	if err != nil {
		return ""
	}
	pin := strings.TrimSpace(string(data))
	if !filepath.IsAbs(pin) {
		return ""
	}
	return pin
}

// writeRootPin best-effort persists root to the pin file: the write is
// skipped when the file already holds the same root, and every error —
// an unwritable directory, a failed write — is ignored silently. The
// pin is a convenience, never a promise: a stale or missing one only
// costs the fallback probes below it.
func writeRootPin(root string) {
	path := rootPinPath()
	if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) == root {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(root+"\n"), 0o644)
}

// rootMarker is the file that marks a directory as a deployment root:
// the kubectl installer inside the assets tree, resolved UNDER the
// candidate directory. Two checkout layouts exist: the module at the
// checkout root (assets/scripts/...) and the module nested in a GO
// directory of a parent checkout (GO/assets/scripts/...). A root is the
// CHECKOUT ROOT, not the module directory itself.
const (
	rootMarker       = "assets/scripts/install-kubectl.sh"
	rootMarkerNested = "GO/" + rootMarker
)

// hasRootMarker reports whether dir carries the deployment-root marker,
// in either layout. The flat probe has one trap: in the nested layout the
// module directory <root>/GO also holds assets/ — and matching it there
// would mistake the module dir for the root (the exact bug the nested
// probe was introduced to avoid). So a bare assets/ tree counts only when
// the parent is not a nested checkout: if <dir>/../GO carries the nested
// marker, dir is that module directory and not a root.
func hasRootMarker(dir string) bool {
	if hasRegularFile(filepath.Join(dir, rootMarkerNested)) {
		return true
	}
	if hasRegularFile(filepath.Join(dir, rootMarker)) {
		parent := filepath.Join(dir, "..")
		return !hasRegularFile(filepath.Join(parent, rootMarkerNested))
	}
	return false
}

// resolveRoot determines the deployment root the way the bash script
// pins SCRIPT_DIR, extended so the binary works "from anywhere". The
// first match wins:
//
//  1. the CHANDLER_ROOT environment variable, verbatim;
//  2. the directory of the executable, when it looks like the checkout —
//     it must carry the root marker (see rootMarker) under it;
//  3. the parent of the executable's directory, same marker file — the
//     normal case: the binary lives inside the checkout (at its root or
//     in release/) and the checkout root one level up matches;
//  4. the pinned root from the pin file (rootPinPath), same marker
//     file — how the `chandler` copy installed on the PATH finds the
//     checkout it was installed from;
//  5. the current working directory, same marker file;
//  6. the parent of the current working directory, same marker file;
//  7. the last-resort default $HOME/chandler.
//
// The root — the repo root — carries config.env and tmp/; the
// installers themselves are dispatched from the materialized embedded
// copy, so a root without a GO/ module only limits those two, never the
// install actions.
//
// Side effect: when a marker-validated candidate wins (2, 3, 5 or 6 —
// not the env override, the pin itself or the default), the root is
// best-effort persisted to the pin file (writeRootPin), so the copy
// installed on the PATH keeps resolving this checkout however it is
// invoked.
func resolveRoot() string {
	root, evidence := resolveRootFrom(
		os.Getenv("CHANDLER_ROOT"),
		readRootPin(),
		exeDir(),
		cwd(),
		home.Dir(),
	)
	switch evidence {
	case evidenceExeDir, evidenceExeDirParent, evidenceCwd, evidenceCwdParent:
		writeRootPin(root)
	}
	return root
}

// resolveRootFrom is resolveRoot's candidate chain. It probes envRoot,
// the marker file under exeDir and under its parent, pinValue, cwd and
// its parent, in that order, and falls back to $home/chandler.
// The returned evidence says which candidate won. Every candidate except
// envRoot and the default must carry the marker file
// (assets/scripts/install-kubectl.sh — see rootMarker); empty
// candidates are skipped, and pinValue only counts when it is absolute —
// the guarantee readRootPin makes for the real pin file.
func resolveRootFrom(envRoot, pinValue, exeDir, cwd, home string) (string, rootEvidence) {
	if envRoot != "" {
		return envRoot, evidenceEnv
	}
	if exeDir != "" {
		if hasRootMarker(exeDir) {
			return exeDir, evidenceExeDir
		}
		// The binary usually sits inside the checkout at
		// <repo-root>/GO/chandler, so the exe-dir probe above
		// fails and one level up — the repo root — carries the marker
		// file. filepath.Join and filepath.Dir both clean, so the probe
		// path and the returned parent are canonical.
		if hasRootMarker(filepath.Join(exeDir, "..")) {
			return filepath.Dir(exeDir), evidenceExeDirParent
		}
	}
	if pinValue != "" && filepath.IsAbs(pinValue) && hasRootMarker(pinValue) {
		return pinValue, evidencePin
	}
	if cwd != "" {
		if hasRootMarker(cwd) {
			return cwd, evidenceCwd
		}
		if hasRootMarker(filepath.Join(cwd, "..")) {
			return filepath.Dir(cwd), evidenceCwdParent
		}
	}
	return filepath.Join(home, "chandler"), evidenceDefault
}

// detectOS maps runtime.GOOS to the friendly OS labels used by
// the bash original's detect_os().
func detectOS() string {
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return "unknown"
	}
}

// requiredTool mirrors one REQUIRED_TOOLS line of
// the bash original: the command to look up plus the package names
// for the brew / apt / dnf / choco manager families (dnfPkg is also
// used for yum / pacman / zypper).
type requiredTool struct {
	cmd      string
	brewPkg  string
	aptPkg   string
	dnfPkg   string
	chocoPkg string
}

// requiredTools lists the tools the install scripts themselves need;
// all four package names match the command name here.
var requiredTools = []requiredTool{
	{cmd: "jq", brewPkg: "jq", aptPkg: "jq", dnfPkg: "jq", chocoPkg: "jq"},
	{cmd: "unzip", brewPkg: "unzip", aptPkg: "unzip", dnfPkg: "unzip", chocoPkg: "unzip"},
	{cmd: "wget", brewPkg: "wget", aptPkg: "wget", dnfPkg: "wget", chocoPkg: "wget"},
}

// requiredToolsFor narrows the list per platform. Windows keeps only jq:
// the scripts download with curl (built into windows and Git Bash),
// extract tarballs with tar and zips with unzip-or-bsdtar (the scripts'
// fallback), and jq itself is direct-download bootstrapped in
// installIfMissing — while wget exists only in the Linux-only browser
// installers, and demanding it sent every Windows session through a
// futile winget attempt.
func requiredToolsFor() []requiredTool {
	if runtime.GOOS == "windows" {
		return requiredTools[:1] // jq
	}
	return requiredTools
}

// errNoPkgManager is returned by installIfMissing when the tool is
// missing and no supported package manager could be detected.
var errNoPkgManager = errors.New("no supported package manager detected")

// installIfMissing ports install_if_missing() from
// the bash original. The ERROR lines for the no-manager case are
// printed here, like bash does; main turns the returned error into the
// process exit code.
func installIfMissing(tool requiredTool) error {
	if _, err := exec.LookPath(tool.cmd); err == nil {
		fmt.Printf("✓ %s is installed.\n", tool.cmd)
		return nil
	}

	fmt.Printf("⚠ %s is not installed.\n", tool.cmd)

	// Windows, jq: skip the package managers entirely and direct-download
	// the official standalone binary into LOCAL_BIN. winget's jq install
	// routinely fails without elevation, and every installer script needs
	// jq, so this is the one prerequisite worth bootstrapping.
	if runtime.GOOS == "windows" && tool.cmd == "jq" {
		fmt.Println("   Installing jq via direct download (github.com/jqlang/jq)...")
		return bootstrapJqWindows()
	}

	manager := pkgmgr.DetectPkgManager(detectOS())
	if manager == "" {
		fmt.Printf("ERROR: %s is required but not found, and no supported package manager was detected.\n", tool.cmd)
		fmt.Printf("       Please install %s manually, then re-run this script.\n", tool.cmd)
		return errNoPkgManager
	}

	fmt.Printf("   Installing %s via %s...\n", tool.cmd, manager)
	return pkgmgr.InstallPkg(manager, tool.brewPkg, tool.aptPkg, tool.dnfPkg, tool.chocoPkg)
}

// exitCodeFor mirrors the bash script's set -e behaviour: exit with the
// failed install command's status, or 1 when there is nothing finer
// (e.g. the no-package-manager case).
func exitCodeFor(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}
	return 1
}

// The jq release the bootstrap installs, with the SHA256 from that
// release's official sha256sum.txt. Pinned rather than
// releases/latest/download so the checksum cannot silently drift with a
// new jq release: a bump is a deliberate change to these two constants.
const (
	jqVersion     = "1.8.2"
	jqSHA256      = "a6fc67fedaf9128a3309a1e2ebb8b986aeccf70122ee46d2cb4849e423f0c627"
	jqDownloadURL = "https://github.com/jqlang/jq/releases/download/jq-" + jqVersion + "/jq-windows-amd64.exe"
)

// bootstrapJqWindows downloads jq's standalone windows binary into
// LOCAL_BIN (jq-windows-amd64.exe — jq publishes no windows/arm64 build,
// so other architectures are a clear error), verifies the SHA256 against
// the pinned official checksum before the file is executed or kept, and
// only then runs `jq --version` as a smoke test. A mismatch or a binary
// that will not run is removed again, leaving nothing broken behind.
func bootstrapJqWindows() error {
	if runtime.GOARCH != "amd64" {
		return fmt.Errorf("no prebuilt jq for windows/%s — install it manually from https://jqlang.github.io/jq/download/", runtime.GOARCH)
	}
	dest := filepath.Join(home.LocalBin(), "jq.exe")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".download"
	resp, err := http.Get(jqDownloadURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download of %s failed: %s", jqDownloadURL, resp.Status)
	}
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}
	sum, err := fileSHA256(tmp)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if sum != jqSHA256 {
		os.Remove(tmp)
		return fmt.Errorf("jq download failed the checksum check (got %s, want %s) — refusing to install", sum, jqSHA256)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	out, err := exec.Command(dest, "--version").Output()
	if err != nil {
		os.Remove(dest)
		return fmt.Errorf("downloaded jq does not run: %w", err)
	}
	fmt.Printf("   jq %s installed to %s (sha256 verified)\n", strings.TrimSpace(string(out)), dest)
	return nil
}

// fileSHA256 returns the hex-encoded SHA256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// k8sStackNotes, cloudProviderNotes and printStackNotes now live in
// internal/ask (notes.go) so the ask flow can re-print them after a
// fresh config.env is saved.

// errNoShellConfig is returned by ensureLocalBinOnPath when neither
// ~/.bashrc nor ~/.zshrc exists to append the PATH block to.
var errNoShellConfig = errors.New("no compatible shell config file found")

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

// ensureLocalBinOnPath ports the "Ensure $LOCAL_BIN is on PATH" block of
// the bash original, which runs after the notes and before the menu.
// LOCAL_BIN is ~/.local/bin under the user's home (home.LocalBin — $HOME,
// with %USERPROFILE% as the windows fallback).
//
// The config file differs per platform:
//
//   - every platform appends the bash export line — inside a chandler
//     marker block — to every existing config among ~/.bashrc and
//     ~/.zshrc (zsh reads only the latter; Git Bash on windows reads
//     .bashrc too);
//   - windows, when neither exists: .bashrc is created (plus the
//     .bash_profile chain Git Bash sources for login shells) and both
//     PowerShell profiles get a $env:Path line — PowerShell never reads
//     .bashrc, and the bash script's hard exit on a missing dotfile would
//     leave a fresh Windows machine with nothing set up. cmd.exe reads
//     neither file and gets a pointer to the system settings instead;
//   - unix, when neither exists: the bash script's behavior — report and
//     fail (errNoShellConfig).
//
// Every write is guarded so running the program twice in a shell that has
// not re-sourced its config cannot pile up duplicate lines.
func ensureLocalBinOnPath() error {
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
		if !fileHasChandlerBlock(c) {
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
			if err := appendPathLineToShellConfig(c, localBin); err != nil {
				return err
			}
		}
	} else if runtime.GOOS == "windows" {
		if err := createWindowsBashConfig(localBin); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not set up a bash config: %v\n", err)
		}
	} else {
		fmt.Println("No compatible shell config file found.")
		return errNoShellConfig
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

// fileHasChandlerBlock reports whether configFile is readable and carries
// a chandler marker block.
func fileHasChandlerBlock(configFile string) bool {
	data, err := os.ReadFile(configFile)
	return err == nil && hasChandlerBlock(string(data))
}

// appendPathLineToShellConfig appends the bash PATH export for localBin
// to configFile inside a chandler marker block, creating the file when
// missing, and skips the append when the file already carries a block
// (see ensureLocalBinOnPath). The written lines stay bash syntax on every
// platform, because the config is a bash (or zsh) file. The block is what
// uninstall strips, so it must be the only thing this write adds.
func appendPathLineToShellConfig(configFile, localBin string) error {
	if fileHasChandlerBlock(configFile) {
		return nil
	}
	f, err := os.OpenFile(configFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	// echo "export PATH=\"$LOCAL_BIN:\$PATH\"" >> "$CONFIG_FILE" — the
	// real path interpolated, the trailing $PATH literal.
	_, err = f.WriteString(fmt.Sprintf("%s\nexport PATH=\"%s:$PATH\"\n%s\n", markerBegin, localBin, markerEnd))
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
	if err := appendPathLineToShellConfig(filepath.Join(home.Dir(), ".bashrc"), localBin); err != nil {
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
		if fileHasChandlerBlock(path) {
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

// materializeAssets extracts the embedded assets into a fresh directory —
// os.MkdirTemp("", "chandler-assets-") — and returns its path. Every run
// materializes its own copy: there is no marker and no reuse, because the
// previous shared temp-dir layout (with its .ok marker) kept serving
// the previous build's installers after a binary upgrade — a fresh dir per
// run makes staleness impossible and concurrent invocations independent.
// The caller owns the directory: on the interactive path main removes it
// right before returning (child installers run synchronously inside the
// menu loop, so the tree is dead once TuiLoop/FallbackMenu return), while
// an os.Exit or a crashed session leaves it for sweepStaleAssets (run
// here first) and the OS temp cleaner.
func materializeAssets() (string, error) {
	sweepStaleAssets()
	dir, err := os.MkdirTemp("", "chandler-assets-")
	if err != nil {
		return "", err
	}
	if err := assets.Extract(dir); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// sweepStaleAssets removes chandler-assets-* directories from the OS temp
// dir that earlier sessions abandoned via an os.Exit path — a crashed
// session's tree is never removed by anyone else, and on windows the temp
// cleaner may never run. Only directories idle for over a day are swept,
// so a concurrent run's live tree is never touched. Best-effort: every
// failure is ignored, a leftover dir is cosmetic.
func sweepStaleAssets() {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "chandler-assets-*"))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, m := range matches {
		if info, err := os.Stat(m); err != nil || !info.IsDir() || info.ModTime().After(cutoff) {
			continue
		}
		os.RemoveAll(m)
	}
}

// childEnvVars lists the environment handed to the child installers, so
// they work from the materialized embedded copy instead of assuming the
// legacy hard-coded deployment-root layout:
//
//   - SCRIPT_DIR: the materialized scripts/ dir (where install-<tool>.sh
//     live and where the menu dispatches to);
//   - SCRIPT_TMPDIR: <root>/tmp — the download scratch space, semantics
//     unchanged, only now always exported;
//   - ROOT: the resolved deployment root (config.env, tmp/);
//   - BASHFILES_DIR: the materialized bash-files/ dir (dotfile copies);
//   - CHANDLER_SELF: the running executable, verbatim, not
//     symlink-resolved — install-bashtools.sh copies it onto the PATH as
//     `chandler` and, seeing it set, leaves the pin file to the Go side
//     (resolveRoot persists it on marker wins).
func exportChildEnv(root, assetsDir, tmpDir string) {
	values := map[string]string{
		"SCRIPT_DIR":    filepath.Join(assetsDir, "scripts"),
		"SCRIPT_TMPDIR": tmpDir,
		"ROOT":          root,
		"BASHFILES_DIR": filepath.Join(assetsDir, "bash-files"),
		"CHANDLER_SELF": exeSelf(),
	}
	for key, value := range values {
		if value != "" {
			os.Setenv(key, value)
		}
	}
}

// usage prints the subcommand summary. help writes it to stdout and
// exits 0; a rejected argument gets it on stderr after a one-line
// error, exiting 1. The program name follows argv[0], so the same text
// reads right as `chandler` (the copy installed on the PATH) and as the
// raw binary name.
func usage(w io.Writer) {
	fmt.Fprintf(w, "Usage: %s [command]\n\n", filepath.Base(os.Args[0]))
	fmt.Fprintf(w, "  (no command)  interactive deployment menu (the default)\n")
	fmt.Fprintf(w, "  update        re-copy the helper scripts and refresh the installed chandler command\n")
	fmt.Fprintf(w, "  install       copy this binary to ~/.local/bin/chandler (sets up the command)\n")
	fmt.Fprintf(w, "  uninstall     remove the installed command, the deployed scripts and every config line chandler wrote\n")
	fmt.Fprintf(w, "  version       print the version (dev for plain `go build`s)\n")
	fmt.Fprintf(w, "  help          show this help\n")
}

// errRefreshSkipped and errRefreshCurrent are ensureCopy's non-failure
// outcomes: nothing to do (the installed file IS the source, or there is
// no usable source), and the installed copy already matches the source.
var (
	errRefreshSkipped = errors.New("self-refresh skipped")
	errRefreshCurrent = errors.New("installed copy already current")
)

// installTarget returns the conventional install target of the `chandler`
// command: ~/.local/bin/chandler (home.LocalBin), with the windows
// executable suffix so the installed command is directly runnable there.
func installTarget() string {
	return filepath.Join(home.LocalBin(), commandName())
}

// commandName is the `chandler` command's file name on this platform:
// `chandler` on unix, `chandler.exe` on windows.
func commandName() string {
	if runtime.GOOS == "windows" {
		return "chandler.exe"
	}
	return "chandler"
}

// checkoutBinary is the conventional name of the built binary inside the
// checkout's GO module — what `go build` (or scripts/build.sh) leaves
// there — with the windows executable suffix.
func checkoutBinary() string {
	if runtime.GOOS == "windows" {
		return "chandler.exe"
	}
	return "chandler"
}

// ensureInstalledCopy best-effort ensures the conventional `chandler`
// command — installTarget, ~/.local/bin/chandler — matches source: the
// checkout binary at <root>/GO/chandler. Unlike the running
// file the old refresh targeted, the installed side need not exist: after
// `update` the canonical command is guaranteed present and current, whether
// that means installed (a deleted or never-installed `chandler` — the
// user-reported gap), replaced, or left alone. When the running file IS
// the target, the rename-over-self below still works; when the running
// file is the source itself, ensureCopy skips. The target's directory is
// created first (0755), so a first-ever install on a fresh machine cannot
// fail for a missing ~/.local/bin. Every hard failure is the caller's
// warning, never the update's exit code.
func ensureInstalledCopy(source string) error {
	target := installTarget()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return ensureCopy(target, source)
}

// ensureCopy makes installed match source's bytes: a differing installed
// file is replaced, a MISSING installed file is installed — the new case
// that turns a deleted `chandler` command into an install instead of an
// error — and matching bytes are reported as current. The new bytes land
// in a temp file beside installed, get the executable bits, and are
// renamed over installed — a rename is atomic and swaps the directory
// entry instead of clobbering a running binary in place (no ETXTBSY);
// rename-over-self, the running copy being the target, works the same
// way. Skips (errRefreshSkipped) when installed IS source or when source
// is missing or not a regular file; errRefreshCurrent when the bytes
// already match; a real error only when an existing installed file cannot
// be read (a directory, wrong permissions) or the replacement cannot be
// written — note the installed file's parent must exist, which
// ensureInstalledCopy and the `install` subcommand guarantee.
func ensureCopy(installed, source string) error {
	if installed == source {
		return errRefreshSkipped
	}
	if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
		return errRefreshSkipped
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
		return errRefreshCurrent
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
		// cannot be deleted; cleanupStaleOld sweeps it after a relaunch).
		if runtime.GOOS == "windows" {
			old := installed + fmt.Sprintf(".old-%d", os.Getpid())
			if err2 := os.Rename(installed, old); err2 == nil {
				if err3 := os.Rename(tmp, installed); err3 == nil {
					_ = os.Remove(old)
					cleanupStaleOld(installed)
					return nil
				}
				os.Rename(old, installed) // rollback: old name back
			}
		}
		os.Remove(tmp)
		return err
	}
	cleanupStaleOld(installed)
	return nil
}

// cleanupStaleOld removes leftover .old-<pid> siblings of installed from
// earlier refreshes whose running image could not be deleted in place —
// after a relaunch they are ordinary dead files. Failures are ignored:
// a leftover .old file is cosmetic, never a functional problem.
func cleanupStaleOld(installed string) {
	matches, err := filepath.Glob(installed + ".old-*")
	if err != nil {
		return
	}
	for _, m := range matches {
		os.Remove(m)
	}
}

// startupAction is what the interactive startup ensure decided to do
// about the installed `chandler` command.
type startupAction int

const (
	startupCurrent startupAction = iota // running as the target, or the bytes already match: a silent no-op
	startupRefresh                      // the target exists and differs: replace it
	startupInstall                      // the target is missing: install it
)

// classifyCommandStartup decides what the interactive startup ensure must
// do for the installed command at target given the running binary at
// source: startupCurrent — silently nothing — when target IS source (the
// launched copy is the installed command itself) or when both files exist
// and match byte-for-byte; startupRefresh when an existing target
// differs; startupInstall when the target is missing (a deleted or
// never-installed `chandler`). A target or source that cannot be read is an
// error, not a guess — the caller warns once and moves on. The comparison
// reads both files whole; nothing is written here, so the common case
// costs two stat/read calls and zero output.
func classifyCommandStartup(target, source string) (startupAction, error) {
	if target == source {
		return startupCurrent, nil
	}
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		return startupInstall, nil
	} else if err != nil {
		return 0, err
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
		return startupCurrent, nil
	}
	return startupRefresh, nil
}

// applyStartupEnsure performs the action classifyCommandStartup chose:
// silent for startupCurrent; a refresh or install through ensureCopy —
// the same atomic temp-file-and-rename the subcommands use — announced
// with one line before and one after. Failures are warned on errW and
// swallowed: a broken ensure must never cost the interactive session its
// exit code. The target's directory is created only when something will
// actually be installed into it.
func applyStartupEnsure(out, errW io.Writer, target, source string, action startupAction) {
	switch action {
	case startupCurrent:
		// The common case: the command is the running binary itself, or an
		// identical copy of it. Zero output, zero writes.
	case startupRefresh:
		fmt.Fprintf(out, "chandler: updating installed command at %s...\n", target)
		if err := ensureCopy(target, source); err != nil {
			fmt.Fprintf(errW, "chandler: refresh failed: %v\n", err)
			return
		}
		fmt.Fprintf(out, "chandler: refreshed from %s\n", source)
	case startupInstall:
		fmt.Fprintf(out, "chandler: installing command to %s...\n", target)
		// A first-ever install on a fresh machine has no ~/.local/bin yet;
		// ensureCopy needs the target's parent to exist.
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fmt.Fprintf(errW, "chandler: install failed: %v\n", err)
			return
		}
		if err := ensureCopy(target, source); err != nil {
			fmt.Fprintf(errW, "chandler: install failed: %v\n", err)
			return
		}
		fmt.Fprintf(out, "chandler: installed to %s\n", target)
	}
}

// ensureCommandOnStartup is the interactive path's best-effort keep-alive
// for the `chandler` command: it classifies installTarget (~/.local/bin/
// chandler, the $HOME-built target) against the running executable VERBATIM
// — not symlink-resolved, so launching through a symlink to the checkout
// binary counts as current — and applies the result. Nothing is printed
// when the command is already current, one line pair when it is installed
// or refreshed, one warning line when the check itself fails; it returns
// nothing and never exits, so the menu always comes up. Only the
// interactive flow calls this — the `update` and `install` subcommands
// keep their own ensure and messages.
func ensureCommandOnStartup(out, errW io.Writer) {
	source, err := os.Executable() // verbatim, NOT symlink-resolved
	if err != nil {
		fmt.Fprintf(errW, "chandler: startup check skipped: %v\n", err)
		return
	}
	source = filepath.Clean(source)
	target := installTarget()
	action, err := classifyCommandStartup(target, source)
	if err != nil {
		fmt.Fprintf(errW, "chandler: startup check failed: %v\n", err)
		return
	}
	applyStartupEnsure(out, errW, target, source, action)
}

// checkoutBinaryPath returns the conventional built binary inside the
// checkout at root — the file `update` refreshes the installed command
// from. The flat layout keeps it at the root; the nested one in GO/.
func checkoutBinaryPath(root string) string {
	if flat := filepath.Join(root, checkoutBinary()); hasRegularFile(flat) {
		return flat
	}
	return filepath.Join(root, "GO", checkoutBinary())
}

// runUpdate is the `update` subcommand: the menu's __update_scripts action
// (item 14 / the u key) without the interactive program around it. The
// root resolves the normal way — the ensure below needs it — and the
// shared update body, pure Go since the assets became embedded, syncs the
// embedded custom-scripts into the deployed scripts tree
// (home.ScriptsDir). config.env is not
// loaded and the prerequisite checks are skipped. UpdateScripts' own
// output is the whole story — no startup lines, no final Done line.
// On top, once UpdateScripts returns, the conventional `chandler` command
// (installTarget, ~/.local/bin/chandler) is ensured from the checkout
// binary at <root>/GO/chandler — the checkout binary's real
// location, inside the GO module the resolved repo root holds — so after
// `update` the command is guaranteed present and current: missing means
// installed, differing means replaced, matching means left alone,
// best-effort and printed, never reflected in the exit code, which stays
// the updater's, the same philosophy as the sync inside it.
func runUpdate() int {
	root := resolveRoot()
	updateErr := menu.UpdateScripts()

	// The ensure runs however the updater fared — the sync half above
	// does the same — and only ever prints or warns on stderr: updateErr
	// stays authoritative. The pre-stat only decides which success line
	// reads true; ensureCopy itself reports the missing-target install as
	// a plain nil.
	source := checkoutBinaryPath(root)
	target := installTarget()
	_, statErr := os.Stat(target)
	targetMissing := errors.Is(statErr, fs.ErrNotExist)
	switch err := ensureInstalledCopy(source); {
	case err == nil:
		if targetMissing {
			fmt.Printf("chandler: installed to %s\n", target)
		} else {
			fmt.Printf("chandler: refreshed from %s\n", source)
		}
	case errors.Is(err, errRefreshCurrent):
		fmt.Println("chandler: already current.")
	case errors.Is(err, errRefreshSkipped):
		// Nothing to do: running from the checkout binary itself, or no
		// usable source beside the resolved root.
	default:
		fmt.Fprintf(os.Stderr, "chandler: refresh failed: %v\n", err)
	}

	if updateErr != nil {
		return exitCodeFor(updateErr)
	}
	return 0
}

// runInstallCmd is the `install` subcommand: it deploys the binary that
// was run to the conventional command target (installTarget,
// ~/.local/bin/chandler), creating the target's directory first. The root
// resolves the normal way — not because the install needs it, but for its
// side effect: running `install` from the checkout records the pin, so
// the copy left at the target keeps resolving that checkout. The
// convention: the canonical artifact <root>/GO/chandler is
// what `update` refreshes from, while `install` deploys whatever binary
// you ran — honest, because a `go build` always carries the complete
// embedded assets.
//
// ensureCopy's two skip reasons are told apart BEFORE the call, so the
// message is accurate: errRefreshSkipped covers both installed==source
// and a missing source, and only the first can mean anything here —
// compare paths up front and report "already running as" for it; a skip
// from a missing source cannot happen for the binary that is running and
// stays a defensive failure.
func runInstallCmd() int {
	resolveRoot()
	source, err := os.Executable() // verbatim, NOT symlink-resolved
	if err != nil {
		fmt.Fprintf(os.Stderr, "chandler: install failed: %v\n", err)
		return exitCodeFor(err)
	}
	target := installTarget()
	if target == filepath.Clean(source) {
		fmt.Printf("chandler: skipped (already running as %s)\n", target)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "chandler: install failed: %v\n", err)
		return exitCodeFor(err)
	}
	// The subcommand also sets the PATH up the way the interactive flow
	// does — `chandler install` on a fresh machine should yield a runnable
	// command in a new shell, not just a file in an untracked directory.
	// Advisory only: an environment this fails on still gets the copy.
	if err := ensureLocalBinOnPath(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not add LOCAL_BIN to the PATH: %v\n", err)
	}
	_, statErr := os.Stat(target)
	wasMissing := errors.Is(statErr, fs.ErrNotExist)
	switch err := ensureCopy(target, source); {
	case err == nil:
		if wasMissing {
			fmt.Printf("chandler: installed to %s\n", target)
		} else {
			fmt.Printf("chandler: refreshed from %s\n", source)
		}
	case errors.Is(err, errRefreshCurrent):
		fmt.Println("chandler: already current.")
	default:
		fmt.Fprintf(os.Stderr, "chandler: install failed: %v\n", err)
		return exitCodeFor(err)
	}
	return 0
}

// runUninstall is the `uninstall` subcommand: install and update in
// reverse, so "remove chandler" is one command — or, read as the manual
// recipe, two rm's and a config cleanup. Everything the app ever deployed
// under the home directory goes:
//
//   - the command itself (installTarget, ~/.local/bin/chandler);
//   - the data root (home.ShareRoot, ~/.local/share/chandler) with the
//     synced custom-scripts tree, plus the pre-data-root
//     ~/.local/bin/scripts when an old install left it behind;
//   - the root-pin file's directory (~/.config/chandler);
//   - the desktop entry and hicolor icons the Linux tarball installs;
//   - the four home dotfiles the Bash-tools installer copies
//     (~/.bash_environment, ~/.bash_function, ~/.aws_environment,
//     ~/.commands_environment) and chandler's ~/.bashrc template, each
//     replaced by its newest <name>_bkp_<timestamp> backup when one
//     exists (that backup is the user's original), removed otherwise;
//   - the ~/.aws/config symlink the installer points at the deployed
//     scripts config, restored from its .aws/config_bkp_* backup;
//   - every chandler marker block in ~/.bashrc, ~/.zshrc,
//     ~/.bash_profile and the PowerShell profiles, plus the bare PATH
//     lines installs before the marker blocks appended.
//
// Nothing else in ~/.aws — credentials, credentials.conf, a real config
// file — is ever touched, and tools the installers dropped into
// ~/.local/bin (fzf, kubectl, jq, ...) are left in place: shared tools
// other programs may use. The desktop entry and icons are attempted on
// every platform — they only exist where the Linux installer put them.
//
// The exit code is 1 only when a removal genuinely failed; missing
// things, skipped strips and the windows running-image case are reported,
// not failed — uninstall is best-effort by design, since a half-broken
// install must still be removable.
func runUninstall() int {
	// No resolveRoot here — its side effect re-writes the root-pin file,
	// and uninstall is about to delete that directory; nothing below
	// needs the deployment root, only the home-relative paths.
	target := installTarget()
	localBin := home.LocalBin()

	// The command. The running image cannot be deleted on windows — but a
	// running image may be RENAMED, so the file moves aside under a name
	// cleanup can't miss and the user gets the one instruction that
	// finishes the job.
	switch err := removeTarget(target); {
	case err == nil:
		fmt.Printf("removed %s\n", target)
	case errors.Is(err, fs.ErrNotExist):
		fmt.Printf("no installed command at %s\n", target)
	case runtime.GOOS == "windows" && isRemoveDenied(err):
		aside := target + fmt.Sprintf(".old-%d", os.Getpid())
		if rerr := os.Rename(target, aside); rerr == nil {
			fmt.Printf("renamed the running %s to %s — exit this program, then delete that file\n", target, aside)
		} else {
			fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", target, err)
		}
	default:
		fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", target, err)
	}
	cleanupStaleOld(target)

	// ~/.aws/config: the Bash-tools installer points (or, under MSYS's
	// copy-semantics ln, copies) it at the deployed scripts config, after
	// backing the user's real config up as .aws/config_bkp_<timestamp>.
	// A chandler symlink is dropped; a regular file whose bytes match the
	// deployed config is chandler's copy. Either way the newest backup —
	// the user's real config — is copied back in its place. This runs
	// before the data root below is removed, while the deployed config is
	// still there to compare against. A real config that matches nothing
	// of ours, a user's own link, and everything else in ~/.aws — the
	// credentials files above all — stay untouched.
	awsConfig := filepath.Join(home.Dir(), ".aws", "config")
	if info, err := os.Lstat(awsConfig); err == nil {
		managed := false
		if info.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(awsConfig); err == nil {
				managed = strings.Contains(target, ".local/share/chandler/scripts") ||
					strings.Contains(target, ".local/bin/scripts")
			}
		} else if info.Mode().IsRegular() {
			managed = fileMatchesDeployedConfig(awsConfig)
		}
		if managed {
			backup := newestBackup(awsConfig + "_bkp_")
			if err := os.Remove(awsConfig); err == nil { // drop link/copy before writing through it
				if backup != "" {
					if err := copyFileContents(backup, awsConfig); err == nil {
						fmt.Printf("restored %s from backup %s (it was chandler-managed)\n", awsConfig, backup)
					} else {
						fmt.Fprintf(os.Stderr, "warning: could not restore %s from %s: %v\n", awsConfig, backup, err)
					}
				} else {
					fmt.Printf("removed the chandler-managed %s (no backup to restore)\n", awsConfig)
				}
			}
		}
	}
	if hasRegularFile(filepath.Join(home.Dir(), ".aws", "credentials")) {
		fmt.Println("left untouched (your AWS credentials): ~/.aws")
	}

	// The data root with the scripts tree, and the pre-data-root scripts
	// location an old install may still hold.
	for _, dir := range []string{home.ShareRoot(), filepath.Join(localBin, "scripts")} {
		switch err := removeTarget(dir); {
		case err == nil:
			fmt.Printf("removed %s\n", dir)
		case errors.Is(err, fs.ErrNotExist):
			fmt.Printf("nothing deployed at %s\n", dir)
		default:
			fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", dir, err)
		}
	}

	// The root-pin file's directory (its only resident is the pin file).
	pinDir := filepath.Dir(rootPinPath())
	switch err := removeTarget(pinDir); {
	case err == nil:
		fmt.Printf("removed %s\n", pinDir)
	case errors.Is(err, fs.ErrNotExist):
	default:
		fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", pinDir, err)
	}

	// The desktop entry and hicolor icons the Linux tarball installs —
	// chandler-named files in the user's own share tree.
	desktop := filepath.Join(home.Dir(), ".local", "share", "applications", commandName()+".desktop")
	if err := removeTarget(desktop); err == nil {
		fmt.Printf("removed %s\n", desktop)
	}
	icons, _ := filepath.Glob(filepath.Join(home.Dir(), ".local", "share", "icons", "hicolor", "*", "apps", commandName()+".png"))
	for _, icon := range icons {
		if err := removeTarget(icon); err == nil {
			fmt.Printf("removed %s\n", icon)
		}
	}
	if runtime.GOOS == "darwin" {
		fmt.Println("note: a Chandler.app dragged into /Applications is removed there, by hand.")
	}

	// The Bash-tools install (menu option 8) goes beyond the two-root
	// layout: it copies chandler's bash assets over four home dotfiles and
	// replaces ~/.bashrc with its template, backing each user original up
	// as <name>_bkp_<timestamp>. Uninstall puts the newest backup back —
	// that is the user's file — and drops chandler's copy; ownership is
	// decided by content (the dotfiles reference the deployed tree, the
	// template sources all four), so a same-named file chandler never
	// wrote is left alone.
	for _, name := range []string{".bash_environment", ".bash_function", ".commands_environment", ".aws_environment"} {
		path := filepath.Join(home.Dir(), name)
		if fileReferencesChandlerScripts(path) {
			restoreNewestBackupOrRemove(path)
		}
	}
	if bashrc := filepath.Join(home.Dir(), ".bashrc"); isChandlerBashrcTemplate(bashrc) {
		if backup := newestBackup(bashrc + "_bkp_"); backup != "" {
			keep := bashrc + ".chandler-" + time.Now().Format("20060102150405")
			if err := os.Rename(bashrc, keep); err == nil {
				fmt.Printf("kept chandler's bashrc template as %s\n", keep)
			}
			if err := copyFileContents(backup, bashrc); err == nil {
				fmt.Printf("restored %s from backup %s\n", bashrc, backup)
			} else {
				fmt.Fprintf(os.Stderr, "warning: could not restore %s from %s: %v\n", bashrc, backup, err)
			}
		} else if err := removeTarget(bashrc); err == nil {
			fmt.Printf("removed %s (chandler's template; no backup to restore)\n", bashrc)
		}
	}

	// ~/.aws/credentials is never touched — see the .aws block above.

	// The shell configs: strip every marker block and every bare legacy
	// PATH line, best-effort with warnings — a config that cannot be
	// rewritten still leaves the rest of the uninstall done. This runs
	// after the restores above so a backup carrying an old block is
	// cleaned too.
	for _, name := range []string{".bashrc", ".zshrc", ".bash_profile"} {
		configFile := filepath.Join(home.Dir(), name)
		if stripped := stripChandlerLines(configFile, localBin); stripped {
			fmt.Printf("cleaned %s\n", configFile)
		}
	}
	if runtime.GOOS == "windows" {
		docs := filepath.Join(home.Dir(), "Documents")
		for _, dir := range []string{"PowerShell", "WindowsPowerShell"} {
			profile := filepath.Join(docs, dir, "Microsoft.PowerShell_profile.ps1")
			if stripped := stripChandlerLines(profile, localBin); stripped {
				fmt.Printf("cleaned %s\n", profile)
			}
		}
	}

	// Shared tools the installers dropped into ~/.local/bin (fzf, kubectl,
	// terraform, ...) and jq: reported when present, never removed — other
	// programs may use them.
	jq := filepath.Join(localBin, "jq.exe")
	if runtime.GOOS != "windows" {
		jq = filepath.Join(localBin, "jq")
	}
	if hasRegularFile(jq) {
		fmt.Printf("left in place (shared tool): %s\n", jq)
	}

	fmt.Println("chandler uninstalled. Please reopen all terminals.")
	return 0
}

// removeTarget removes the file or directory at path. An absent path is
// reported as fs.ErrNotExist so callers can phrase "removed" and "was
// never there" differently; RemoveAll alone cannot tell the two apart,
// since it returns nil for both.
func removeTarget(path string) error {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.ErrNotExist
		}
		return err
	}
	return os.RemoveAll(path)
}

// isRemoveDenied reports whether err looks like the windows "file is open
// or in use" failure — the running executable's own image cannot be
// deleted, which is the one expected removeTarget failure.
func isRemoveDenied(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "access is denied") ||
		strings.Contains(strings.ToLower(err.Error()), "being used by another process")
}

// stripChandlerLines rewrites configFile without chandler's additions:
// every marker block (markers included) and every bare legacy PATH line
// naming localBin — the bash export and the PowerShell $env:Path
// assignment from installs before the marker blocks. Comparisons are on
// trimmed lines, so CRLF endings match too. The file is left untouched
// (false returned) when it is unreadable or holds nothing of ours; a
// strip that leaves it empty still writes, because an empty config is
// the honest result.
func stripChandlerLines(configFile, localBin string) bool {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return false
	}
	legacyBash := fmt.Sprintf(`export PATH="%s:$PATH"`, localBin)
	legacyPS := fmt.Sprintf(`$env:Path = "%s;$env:Path"`, localBin)

	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	stripped := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == markerBegin:
			inBlock = true
			stripped = true
			continue
		case trimmed == markerEnd:
			inBlock = false
			stripped = true
			continue
		case inBlock:
			stripped = true
			continue
		case trimmed == legacyBash || trimmed == legacyPS:
			stripped = true
			continue
		}
		out = append(out, line)
	}
	if !stripped {
		return false
	}
	rest := strings.Join(out, "\n")
	if strings.TrimSpace(rest) == "" {
		// Nothing of the user's remains: the file only ever held chandler
		// lines (the profiles and .bashrc a fresh-machine install created),
		// so it goes entirely instead of lingering empty.
		if err := os.Remove(configFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: could not remove the emptied %s: %v\n", configFile, err)
			return false
		}
		return true
	}
	if err := os.WriteFile(configFile, []byte(rest), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not rewrite %s: %v\n", configFile, err)
		return false
	}
	return true
}

// fileReferencesChandlerScripts reports whether the file at path exists
// and mentions the deployed or the legacy scripts tree — the signature of
// the dotfiles the Bash-tools installer copies: their aliases and
// functions point into the tree, so they turn dead when it is removed.
// A same-named file that never mentions it (the .aws_environment
// placeholder ships as comments only) is not chandler's to remove.
func fileReferencesChandlerScripts(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, ".local/share/chandler/scripts") ||
		strings.Contains(s, ".local/bin/scripts")
}

// isChandlerBashrcTemplate reports whether the file at path is the
// .bashrc the Bash-tools installer writes: it sources chandler's four
// home dotfiles by name — a signature an ordinary user config does not
// carry.
func isChandlerBashrcTemplate(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(data)
	for _, name := range []string{".bash_environment", ".bash_function", ".aws_environment", ".commands_environment"} {
		if !strings.Contains(s, name) {
			return false
		}
	}
	return true
}

// fileMatchesDeployedConfig reports whether the file at path is
// byte-identical to the deployed scripts config in either the current
// data root or the legacy location — the signature of the copy MSYS's
// ln makes when the Bash-tools installer "links" ~/.aws/config on
// windows.
func fileMatchesDeployedConfig(path string) bool {
	current, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, deployed := range []string{
		filepath.Join(home.ScriptsDir(), "config"),
		filepath.Join(home.LocalBin(), "scripts", "config"),
	} {
		if data, err := os.ReadFile(deployed); err == nil && bytes.Equal(current, data) {
			return true
		}
	}
	return false
}

// newestBackup returns the most recently modified file matching
// <prefix>* — the newest <name>_bkp_<timestamp> backup the installers
// write before overwriting a user file — or "" when none exists.
func newestBackup(prefix string) string {
	matches, err := filepath.Glob(prefix + "*")
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		fi, erri := os.Stat(matches[i])
		fj, errj := os.Stat(matches[j])
		if erri != nil || errj != nil {
			return false
		}
		return fi.ModTime().After(fj.ModTime())
	})
	return matches[0]
}

// restoreNewestBackupOrRemove reverses a Bash-tools install of one home
// dotfile: the newest <path>_bkp_<timestamp> — the user's original,
// backed up before the installer overwrote it — is copied back; with no
// backup the chandler-installed copy is removed. Call only when the file
// is chandler-managed (fileReferencesChandlerScripts).
func restoreNewestBackupOrRemove(path string) {
	if backup := newestBackup(path + "_bkp_"); backup != "" {
		if err := copyFileContents(backup, path); err == nil {
			fmt.Printf("restored %s from backup %s\n", path, backup)
		} else {
			fmt.Fprintf(os.Stderr, "warning: could not restore %s from %s: %v\n", path, backup, err)
		}
		return
	}
	if err := removeTarget(path); err == nil {
		fmt.Printf("removed %s (chandler-installed, no backup to restore)\n", path)
	}
}

// copyFileContents copies src's bytes to dst (configs only — 0644).
func copyFileContents(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// The subcommand dispatch in front of the interactive default: `update`,
// `install`, the help spellings, and anything else rejected with the
// usage. With no argument main falls through to the interactive menu
// exactly as before — the dispatch runs ahead of the config load and the
// prerequisite checks, so the subcommands never pay for the interactive
// front end.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "update":
			os.Exit(runUpdate())
		case "install":
			os.Exit(runInstallCmd())
		case "uninstall":
			os.Exit(runUninstall())
		case "claim":
			// Deliberately absent from the usage block: the author's
			// sealed identity claim (internal/claim). The blob ships in
			// every binary; opening it requires the 32-byte sealing key,
			// so being able to open it is the authorship proof.
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "claim: needs the key: chandler claim <keyfile>")
				os.Exit(1)
			}
			key, err := os.ReadFile(os.Args[2])
			if err != nil {
				fmt.Fprintf(os.Stderr, "claim: %v\n", err)
				os.Exit(1)
			}
			text, err := claim.Claim(key)
			if err != nil {
				fmt.Fprintln(os.Stderr, "claim: this key does not open the seal")
				os.Exit(1)
			}
			fmt.Print(text)
			return
		case "version":
			fmt.Printf("chandler %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
			return
		case "-h", "--help", "help":
			usage(os.Stdout)
			return
		default:
			if arg := os.Args[1]; strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "unknown option: %s\n", arg)
			} else {
				fmt.Fprintf(os.Stderr, "unknown command: %s\n", arg)
			}
			usage(os.Stderr)
			os.Exit(1)
		}
	}

	root := resolveRoot()

	// Source config.env from the deployment root into the process
	// environment first, so the notes below — and later child
	// installers — see CHANDLER_K8S_STACKS / CHANDLER_CLOUD_PROVIDERS.
	if err := config.Load(root); err != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring unreadable config.env: %v\n", err)
	}

	fmt.Printf("Detected OS: %s\n", detectOS())
	fmt.Printf("Deployment root: %s\n", root)

	fmt.Println()
	fmt.Println("Checking prerequisites...")

	// The bash script exits (set -e) the moment a prerequisite cannot be
	// installed — on windows that closed the window instantly whenever jq
	// was missing without a usable package manager. The menu itself runs
	// fine without these tools (only the child installers need them), so
	// the interactive path warns and continues instead of exiting.
	var prereqFailed bool
	for _, tool := range requiredToolsFor() {
		if err := installIfMissing(tool); err != nil {
			prereqFailed = true
		}
	}
	if prereqFailed {
		fmt.Println("warning: some prerequisites are missing; the install actions may fail until they are available.")
	}

	ask.PrintStackNotes()

	fmt.Println()

	// Ensure $LOCAL_BIN is on PATH (bash: after the notes, before the
	// menu), then create SCRIPT_TMPDIR.
	if err := ensureLocalBinOnPath(); err != nil {
		os.Exit(exitCodeFor(err))
	}

	tmpDir := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot create %s: %v\n", tmpDir, err)
		os.Exit(1)
	}

	// Materialize the embedded assets for the child bash scripts and
	// export the layout they expect (SCRIPT_DIR, SCRIPT_TMPDIR, ROOT,
	// BASHFILES_DIR, CHANDLER_SELF — see exportChildEnv). A failed
	// extraction means a broken binary or an unusable temp dir: there is
	// nothing the menu could still do, so exit rather than limp on.
	assetsDir, err := materializeAssets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: cannot materialize the embedded assets: %v\n", err)
		os.Exit(1)
	}
	// The materialized tree is this run's own (a fresh MkdirTemp) and no
	// longer needed once the menu loop returns — child installers run
	// synchronously inside it — so remove it right before main returns.
	// The os.Exit paths behind the menu (an invalid fallback option, the
	// TUI's Ctrl-C) skip this defer: a session that ends that way leaves
	// one chandler-assets-* dir for the OS temp cleaner.
	defer os.RemoveAll(assetsDir)
	exportChildEnv(root, assetsDir, tmpDir)

	// bash MAIN re-runs detect_pkg_manager after setup_colors/tui_geometry
	// for the menu's status line; an empty result renders as "pkg: none".
	pkgMgr := pkgmgr.DetectPkgManager(detectOS())

	// One keystroke Reader for the whole program, constructed once and
	// handed down sequentially — the stack/cloud questions below and the
	// menu loop both read this instance, mirroring bash's single input
	// stream: an escape window left open when a phase ends parks its
	// follow-up byte here, and the next phase's first read drains it.
	rd := term.NewReaderStdin()

	// The bash script asks the stack/cloud questions just before the menu
	// when no config.env exists yet, and follows them with a blank line.
	if !hasRegularFile(filepath.Join(root, "config.env")) {
		if _, err := ask.AskStackCloud(root, rd); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		fmt.Println()
	}

	// Best-effort keep the installed `chandler` command current at every
	// interactive launch (after the PATH block, before the menu): a
	// command deleted after an install is re-created here, a stale one
	// refreshed, and an already-current one costs two stat/read calls and
	// zero output. Warnings only — never an exit; the menu must come up
	// however the ensure fares.
	ensureCommandOnStartup(os.Stdout, os.Stderr)

	// The full-screen TUI when the terminal can hold it, the plain numeric
	// prompt otherwise. scriptDir is the bash SCRIPT_DIR: the materialized
	// embedded scripts/ directory, where the install-<tool>.sh the actions
	// dispatch to live — the one wiring change the assets-embedding brings
	// to the menu itself.
	scriptDir := filepath.Join(assetsDir, "scripts")
	if menu.UseTUI() {
		menu.TuiLoop(detectOS(), pkgMgr, root, scriptDir, rd)
	} else {
		// A windows session that reaches the fallback menu because stdin
		// is not a console handle gets a pointer to the consoles where the
		// interactive interface works: Git Bash's mintty pipes stdio
		// instead of handing out console handles (winpty bridges that),
		// and redirected output means no full screen either way.
		if runtime.GOOS == "windows" && !term.IsTTY(os.Stdin.Fd()) {
			fmt.Println()
			fmt.Println("Tip: the interactive interface needs a Windows console. Run from Windows Terminal, PowerShell or cmd — or prefix the command with `winpty` in Git Bash.")
		}
		menu.FallbackMenu(detectOS(), pkgMgr, root, scriptDir)
	}

	// bash MAIN's final line; falling off main exits 0.
	fmt.Println("Done. Please reopen all terminals.")
}
