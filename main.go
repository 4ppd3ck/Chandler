// Command chandler is the Go port of the repo's bash
// entrypoint (the bash original): a self-contained deployment tool
// that resolves the deployment root, checks its prerequisites, asks the
// stack/cloud questions into config.env, and offers a full-screen TUI
// (a plain numeric menu on non-terminals) whose entries install the
// k8s/cloud tool set via bash installers. The history of the port lives
// in CHANGELOG.md; the platform support matrix lives in README.md.
//
// Layout: main.go (startup, prerequisite checks, subcommands: update,
// install, tool, doctor, update-check, uninstall dispatch) with the
// concerns split into internal/ packages — rootpin (deployment-root
// resolution + pin), pathsetup (~/.local/bin on the PATH), selfinstall
// (the installed-command keep-alive and the atomic self-refresh),
// uninstall (the removal), assets/ (embedded installers, dotfiles,
// custom scripts), term (per-OS console: raw mode, tty probe, window
// size, keystroke decoding), menu (TUI + action runner + bash
// resolution), ask (stack/cloud pickers), config (config.env), pkgmgr
// (per-OS package managers), customsync (helper-script sync), home (the
// user's home on every platform).

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"chandler/assets"
	"chandler/internal/ask"
	"chandler/internal/claim"
	"chandler/internal/config"
	"chandler/internal/home"
	"chandler/internal/menu"
	"chandler/internal/pathsetup"
	"chandler/internal/pkgmgr"
	"chandler/internal/rootpin"
	"chandler/internal/selfinstall"
	"chandler/internal/term"
	"chandler/internal/uninstall"
)

// version is stamped at release build time via -ldflags
// -X main.version=<v> (release.sh); a plain `go build` reports dev.
var version = "dev"

// releasesAPI is the GitHub releases API endpoint `chandler update-check`
// compares this build against. It is empty for a plain `go build` — set
// it at publish time with
//
//	-ldflags "-X main.releasesAPI=https://api.github.com/repos/<owner>/<repo>/releases/latest"
//
// or point CHANDLER_RELEASES_API at the endpoint for a single run.
const releasesAPI = ""

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

// detectOS maps runtime.GOOS to the friendly OS labels used by
// the bash original's detect_os(); freebsd is added over the bash list
// so its pkg manager can be probed.
func detectOS() string {
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	case "freebsd":
		return "freebsd"
	default:
		return "unknown"
	}
}

// requiredTool mirrors one REQUIRED_TOOLS line of
// the bash original: the command to look up plus the package names
// for the brew / apt / dnf / choco manager families (dnfPkg is also
// used for yum / pacman / zypper / pkg).
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

// ensurePrerequisites runs the install-if-missing pass for the installer
// tools, printing the ✓/⚠ lines like the bash script. It never exits:
// the returned slice names the tools that could not be provided, so the
// interactive flow warns and continues (only the child installers need
// these tools) — and the menu's status line shows the honest ⚠ list
// instead of claiming readiness. The `tool` subcommand ignores it.
func ensurePrerequisites() []string {
	var missing []string
	for _, tool := range requiredToolsFor() {
		if err := installIfMissing(tool); err != nil {
			missing = append(missing, tool.cmd)
		}
	}
	if len(missing) > 0 {
		fmt.Println("warning: some prerequisites are missing; the install actions may fail until they are available.")
	}
	return missing
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

// jqHTTPClient caps the jq bootstrap download — a bare http.Get could
// hang the whole startup forever on a stuck connection. Generous, since
// it is a real binary download; still bounded.
var jqHTTPClient = &http.Client{Timeout: 5 * time.Minute}

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
	resp, err := jqHTTPClient.Get(jqDownloadURL)
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
	fmt.Fprintf(w, "  tool <name>   install one tool non-interactively (kubectl, k9s, helm, terraform,\n")
	fmt.Fprintf(w, "                aws, oc, brave, helium, bashtools, fzf-setup, all, core); -y skips\n")
	fmt.Fprintf(w, "                the bulk confirm, `-- <args>` reaches the installer (a version:\n")
	fmt.Fprintf(w, "                `tool kubectl -- 1.31.0`), `tool` alone lists the names\n")
	fmt.Fprintf(w, "  doctor        print a read-only status report (root, config, PATH, tools)\n")
	fmt.Fprintf(w, "  update-check  compare this build against the configured release feed\n")
	fmt.Fprintf(w, "  uninstall     remove the installed command, the deployed scripts and every config line chandler wrote\n")
	fmt.Fprintf(w, "  version       print the version (dev for plain `go build`s)\n")
	fmt.Fprintf(w, "  help          show this help\n")
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
// (selfinstall.InstallTarget, ~/.local/bin/chandler) is ensured from the
// checkout binary at <root>/GO/chandler — the checkout binary's real
// location, inside the GO module the resolved repo root holds — so after
// `update` the command is guaranteed present and current: missing means
// installed, differing means replaced, matching means left alone,
// best-effort and printed, never reflected in the exit code, which stays
// the updater's, the same philosophy as the sync inside it.
func runUpdate() int {
	root := rootpin.Resolve()
	updateErr := menu.UpdateScripts()

	// The ensure runs however the updater fared — the sync half above
	// does the same — and only ever prints or warns on stderr: updateErr
	// stays authoritative. The pre-stat only decides which success line
	// reads true; EnsureCopy itself reports the missing-target install as
	// a plain nil.
	source := selfinstall.CheckoutBinaryPath(root)
	target := selfinstall.InstallTarget()
	_, statErr := os.Stat(target)
	targetMissing := errors.Is(statErr, fs.ErrNotExist)
	switch err := selfinstall.EnsureInstalledCopy(source); {
	case err == nil:
		if targetMissing {
			fmt.Printf("chandler: installed to %s\n", target)
		} else {
			fmt.Printf("chandler: refreshed from %s\n", source)
		}
	case errors.Is(err, selfinstall.ErrRefreshCurrent):
		fmt.Println("chandler: already current.")
	case errors.Is(err, selfinstall.ErrRefreshSkipped):
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
// was run to the conventional command target (selfinstall.InstallTarget,
// ~/.local/bin/chandler), creating the target's directory first. The root
// resolves the normal way — not because the install needs it, but for its
// side effect: running `install` from the checkout records the pin, so
// the copy left at the target keeps resolving that checkout. The
// convention: the canonical artifact <root>/GO/chandler is
// what `update` refreshes from, while `install` deploys whatever binary
// you ran — honest, because a `go build` always carries the complete
// embedded assets.
//
// EnsureCopy's two skip reasons are told apart BEFORE the call, so the
// message is accurate: ErrRefreshSkipped covers both installed==source
// and a missing source, and only the first can mean anything here —
// compare paths up front and report "already running as" for it; a skip
// from a missing source cannot happen for the binary that is running and
// stays a defensive failure.
func runInstallCmd() int {
	rootpin.Resolve()
	source, err := os.Executable() // verbatim, NOT symlink-resolved
	if err != nil {
		fmt.Fprintf(os.Stderr, "chandler: install failed: %v\n", err)
		return exitCodeFor(err)
	}
	target := selfinstall.InstallTarget()
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
	if err := pathsetup.EnsureLocalBinOnPath(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not add LOCAL_BIN to the PATH: %v\n", err)
	}
	_, statErr := os.Stat(target)
	wasMissing := errors.Is(statErr, fs.ErrNotExist)
	switch err := selfinstall.EnsureCopy(target, source); {
	case err == nil:
		if wasMissing {
			fmt.Printf("chandler: installed to %s\n", target)
		} else {
			fmt.Printf("chandler: refreshed from %s\n", source)
		}
		// The PATH write above (or the earlier install that made the
		// configs current) does not reach already-open shells: without the
		// pointer the command looks dead until a lucky new terminal.
		fmt.Println("chandler: open a new terminal (or re-source your shell config) to use the command.")
	case errors.Is(err, selfinstall.ErrRefreshCurrent):
		fmt.Println("chandler: already current.")
	default:
		fmt.Fprintf(os.Stderr, "chandler: install failed: %v\n", err)
		return exitCodeFor(err)
	}
	return 0
}

// toolProbe describes one installed-tool line of the doctor report: the
// command to look up and the argv that yields a one-line version (nil
// args = presence only, no version probe).
type toolProbe struct {
	name string
	args []string
}

// doctorProbes lists the tools the report looks for, in the menu's
// install order.
var doctorProbes = []toolProbe{
	{"kubectl", []string{"version", "--client"}},
	{"k9s", []string{"version", "--short"}},
	{"helm", []string{"version", "--short"}},
	{"terraform", []string{"version"}},
	{"aws", []string{"--version"}},
	{"oc", []string{"version", "--client"}},
	{"fzf", []string{"--version"}},
	{"jq", []string{"--version"}},
	{"kubectx", nil},
	{"kubens", nil},
}

// firstOutputLine runs cmd with a short timeout and returns its first
// non-empty stdout line, trimmed — the version-string shape every tool
// here prints. Lines with ANSI escapes are skipped: some tools (k9s)
// lead with a colored banner before the version line. Errors (missing
// tool, timeout, nonzero exit) read as "".
func firstOutputLine(name string, args []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.ContainsRune(line, '\x1b') {
			continue
		}
		return line
	}
	return ""
}

// doctorTool is one tool or prerequisite line of the doctor report.
type doctorTool struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
}

// doctorShellConfig is one shell-config line of the doctor report.
type doctorShellConfig struct {
	Path  string `json:"path"`
	Block bool   `json:"chandler_block"`
}

// doctorReport is the doctor subcommand's data, probed first and
// rendered second: as text by default, as JSON with --json — the same
// facts either way, for scripts and dotfiles integration.
type doctorReport struct {
	Version          string              `json:"version"`
	GOOS             string              `json:"goos"`
	GOARCH           string              `json:"goarch"`
	OS               string              `json:"os"`
	DeploymentRoot   string              `json:"deployment_root"`
	RootPinPath      string              `json:"root_pin_path"`
	RootPin          string              `json:"root_pin,omitempty"`
	ConfigStacks     string              `json:"config_k8s_stacks,omitempty"`
	ConfigClouds     string              `json:"config_cloud_providers,omitempty"`
	LocalBin         string              `json:"local_bin"`
	LocalBinOnPath   bool                `json:"local_bin_on_path"`
	Shell            string              `json:"shell"`
	ShellConfigs     []doctorShellConfig `json:"shell_configs"`
	Prerequisites    []doctorTool        `json:"prerequisites"`
	Tools            []doctorTool        `json:"tools"`
	ScriptsDir       string              `json:"scripts_dir"`
	ScriptsDeployed  bool                `json:"scripts_deployed"`
	CommandPath      string              `json:"command_path"`
	CommandInstalled bool                `json:"command_installed"`
}

// buildDoctorReport probes everything the doctor reports — the same
// checks the interactive startup makes, read-only.
func buildDoctorReport() doctorReport {
	root := rootpin.Resolve()
	if err := config.Load(root); err != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring unreadable config.env: %v\n", err)
	}
	r := doctorReport{
		Version:        version,
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		OS:             detectOS(),
		DeploymentRoot: root,
		RootPinPath:    rootpin.RootPinPath(),
		RootPin:        rootpin.ReadRootPin(),
		ConfigStacks:   os.Getenv(config.KeyK8sStacks),
		ConfigClouds:   os.Getenv(config.KeyCloudProviders),
		LocalBin:       home.LocalBin(),
		ScriptsDir:     home.ScriptsDir(),
		CommandPath:    selfinstall.InstallTarget(),
	}

	sep := string(os.PathListSeparator)
	r.LocalBinOnPath = strings.Contains(sep+os.Getenv("PATH")+sep, sep+r.LocalBin+sep)
	r.Shell = pathsetup.LoginShellName()

	for _, name := range []string{".bashrc", ".zshrc"} {
		configFile := filepath.Join(home.Dir(), name)
		if !rootpin.HasRegularFile(configFile) {
			continue
		}
		r.ShellConfigs = append(r.ShellConfigs, doctorShellConfig{
			Path:  configFile,
			Block: pathsetup.FileHasChandlerBlock(configFile),
		})
	}
	// fish's config joins the scan: a fish user's block lives there, and
	// the report's fish hint below reads this entry's status.
	if fishConf := pathsetup.FishConfigPath(); rootpin.HasRegularFile(fishConf) {
		r.ShellConfigs = append(r.ShellConfigs, doctorShellConfig{
			Path:  fishConf,
			Block: pathsetup.FileHasChandlerBlock(fishConf),
		})
	}

	for _, tool := range requiredToolsFor() {
		_, err := exec.LookPath(tool.cmd)
		r.Prerequisites = append(r.Prerequisites, doctorTool{Name: tool.cmd, Installed: err == nil})
	}

	for _, probe := range doctorProbes {
		t := doctorTool{Name: probe.name}
		if _, err := exec.LookPath(probe.name); err == nil {
			t.Installed = true
			if probe.args != nil {
				t.Version = firstOutputLine(probe.name, probe.args)
			}
		}
		r.Tools = append(r.Tools, t)
	}

	if info, err := os.Stat(r.ScriptsDir); err == nil && info.IsDir() {
		r.ScriptsDeployed = true
	}
	r.CommandInstalled = rootpin.HasRegularFile(r.CommandPath)
	return r
}

// fishBlockPresent reports whether the report's shell-config scan found a
// chandler block in the fish config — the entry only exists when the
// config file does, so absence means unwired fish too.
func fishBlockPresent(r doctorReport) bool {
	fishConf := pathsetup.FishConfigPath()
	for _, sc := range r.ShellConfigs {
		if sc.Path == fishConf {
			return sc.Block
		}
	}
	return false
}

// runDoctor is the `doctor` subcommand: a read-only report of everything
// the interactive startup would check — OS, deployment root and pin,
// config.env, the PATH and the shell-config blocks, prerequisites,
// installed tools with their versions, the deployed scripts tree and the
// installed command. It installs nothing, exits 0; problems are ⚠ lines.
// --json renders the same facts machine-readably.
func runDoctor(args []string) int {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json", "-j":
			asJSON = true
		case "-h", "--help":
			fmt.Println("Usage: chandler doctor [--json]")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "doctor: unknown option: %s\n", a)
			return 1
		}
	}
	r := buildDoctorReport()

	if asJSON {
		data, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "doctor: cannot marshal the report: %v\n", err)
			return 1
		}
		fmt.Println(string(data))
		return 0
	}

	fmt.Printf("chandler %s (%s/%s)\n", r.Version, r.GOOS, r.GOARCH)
	fmt.Printf("os:              %s\n", r.OS)
	fmt.Printf("deployment root: %s\n", r.DeploymentRoot)
	if r.RootPin != "" {
		fmt.Printf("root pin:        %s -> %s\n", r.RootPinPath, r.RootPin)
	} else {
		fmt.Printf("root pin:        none (%s)\n", r.RootPinPath)
	}
	fmt.Printf("config.env:      stacks=%q clouds=%q\n", r.ConfigStacks, r.ConfigClouds)
	if r.LocalBinOnPath {
		fmt.Printf("path:            ✓ %s is on the PATH\n", r.LocalBin)
	} else {
		fmt.Printf("path:            ⚠ %s is NOT on the PATH\n", r.LocalBin)
	}
	fmt.Printf("shell:           %s\n", r.Shell)
	for _, sc := range r.ShellConfigs {
		state := "no chandler block"
		if sc.Block {
			state = "chandler block present"
		}
		fmt.Printf("shell config:    %s (%s)\n", sc.Path, state)
	}
	// fish reads neither .bashrc nor .zshrc — its block alone decides
	// whether a fish session sees the command. The doctor only reports;
	// the fix is any chandler run, which wires config.fish on the way.
	if r.Shell == "fish" && !fishBlockPresent(r) {
		fmt.Printf("shell config:    ⚠ fish does not read .bashrc/.zshrc — no chandler block in %s; run any chandler command once to wire it\n", pathsetup.FishConfigPath())
	}

	fmt.Println()
	fmt.Println("Prerequisites (checked, not installed):")
	for _, tool := range r.Prerequisites {
		if tool.Installed {
			fmt.Printf("  ✓ %s\n", tool.Name)
		} else {
			fmt.Printf("  ⚠ %s missing — install actions using it will fail\n", tool.Name)
		}
	}

	fmt.Println()
	fmt.Println("Installed tools:")
	for _, tool := range r.Tools {
		if !tool.Installed {
			fmt.Printf("  ✗ %-10s not installed\n", tool.Name)
			continue
		}
		line := tool.Version
		if line == "" {
			line = "present"
		}
		fmt.Printf("  ✓ %-10s %s\n", tool.Name, line)
	}

	fmt.Println()
	if r.ScriptsDeployed {
		fmt.Printf("scripts tree:    deployed at %s\n", r.ScriptsDir)
	} else {
		fmt.Printf("scripts tree:    not deployed (run the Bash-tools install or `chandler update`)\n")
	}
	if r.CommandInstalled {
		fmt.Printf("command:         installed at %s\n", r.CommandPath)
	} else {
		fmt.Printf("command:         not installed (%s)\n", r.CommandPath)
	}
	return 0
}

// parseToolArgs splits a `chandler tool` argument list into the tool
// name, the -y flag, and the `--`-separated tail that is forwarded
// verbatim to the installer script — a version for the prompting
// installers (`chandler tool kubectl -- 1.31.0`), an arch or choice for
// the ones that already take a first argument.
func parseToolArgs(args []string) (name string, assumeYes bool, extra []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			extra = append(extra, args[i+1:]...)
			break
		}
		switch a {
		case "-y", "--yes":
			assumeYes = true
		case "--list":
			name = "list"
		case "-h", "--help":
			name = "help"
		default:
			if strings.HasPrefix(a, "-") {
				return "", false, nil, fmt.Errorf("unknown option: %s", a)
			}
			if name != "" {
				return "", false, nil, fmt.Errorf("one tool per run (got %q and %q)", name, a)
			}
			name = a
		}
	}
	return name, assumeYes, extra, nil
}

// runToolCmd is the `tool` subcommand: install one menu entry
// non-interactively — `chandler tool kubectl`, `chandler tool all` for
// the bulk install (its confirm is skipped with -y), an installer
// argument after `--` (a version: `chandler tool kubectl -- 1.31.0`).
// The flow mirrors the interactive startup (root, config.env,
// prerequisites, PATH, materialized assets, child env) and then runs
// the same RunAction the menu would, so the menu and the command line
// share one behavior.
func runToolCmd(args []string) int {
	name, assumeYes, extraArgs, err := parseToolArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tool: %v\n", err)
		usage(os.Stderr)
		return 1
	}
	known := menu.KnownToolActions()
	if name == "help" {
		fmt.Println("Usage: chandler tool <name> [-y] [-- <installer args>]")
		fmt.Println("Names: " + strings.Join(known, ", "))
		return 0
	}
	if name == "" || name == "list" {
		fmt.Println("Known tools:")
		for _, t := range known {
			fmt.Printf("  %s\n", t)
		}
		if name != "" {
			return 0
		}
		fmt.Println("\nUsage: chandler tool <name> [-y] [-- <installer args>]")
		fmt.Println("       (`all` and `core` run the bulk installs; e.g. `tool kubectl -- 1.31.0`)")
		return 1
	}
	valid := false
	for _, k := range known {
		if k == name {
			valid = true
			break
		}
	}
	if !valid {
		fmt.Fprintf(os.Stderr, "unknown tool: %s (known: %s)\n", name, strings.Join(known, ", "))
		return 1
	}
	if len(extraArgs) > 0 && (name == "all" || name == "core") {
		fmt.Fprintln(os.Stderr, "tool: extra arguments only apply to a single tool run")
		return 1
	}
	action := name
	if name == "all" {
		action = "__all"
	} else if name == "core" {
		action = "__core"
	}

	root := rootpin.Resolve()
	if err := config.Load(root); err != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring unreadable config.env: %v\n", err)
	}
	fmt.Printf("Detected OS: %s\n", detectOS())
	fmt.Printf("Deployment root: %s\n", root)
	ensurePrerequisites()
	if err := pathsetup.EnsureLocalBinOnPath(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not add LOCAL_BIN to the PATH: %v\n", err)
	}

	tmpDir := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: cannot create %s: %v\n", tmpDir, err)
		return 1
	}
	assetsDir, err := materializeAssets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: cannot materialize the embedded assets: %v\n", err)
		return 1
	}
	defer os.RemoveAll(assetsDir)
	exportChildEnv(root, assetsDir, tmpDir)

	rd := term.NewReaderStdin()
	if err := menu.RunTool(action, root, filepath.Join(assetsDir, "scripts"), assumeYes, extraArgs, rd); err != nil {
		return exitCodeFor(err)
	}
	fmt.Println("Done. Please reopen all terminals.")
	return 0
}

// compareVersions compares two dotted version strings (a leading v is
// ignored): numerically per dot-part while both parts are numeric, by
// string otherwise. Best-effort — it exists to tell "same" from "newer"
// for the release feed, not to order arbitrary semver ranges.
func compareVersions(a, b string) int {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		av, bv := "", ""
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		an, aerr := strconv.Atoi(av)
		bn, berr := strconv.Atoi(bv)
		if aerr == nil && berr == nil {
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
			continue
		}
		if c := strings.Compare(av, bv); c != 0 {
			return c
		}
	}
	return 0
}

// updateCheckHTTPClient caps the release-feed fetch.
var updateCheckHTTPClient = &http.Client{Timeout: 10 * time.Second}

// runUpdateCheck is the `update-check` subcommand: explicitly fetch the
// configured release feed (releasesAPI, or CHANDLER_RELEASES_API for one
// run), compare its latest tag against this build's version and report.
// Never runs on startup. Exit codes: 0 current/dev/unconfigured, 1 fetch
// or parse error, 2 a newer release is available (script-friendly).
func runUpdateCheck() int {
	if version == "dev" {
		fmt.Println("chandler: dev build — nothing to compare against a release feed.")
		return 0
	}
	api := os.Getenv("CHANDLER_RELEASES_API")
	if api == "" {
		api = releasesAPI
	}
	if api == "" {
		fmt.Println("chandler: no release feed configured for this build.")
		fmt.Println("   (set -ldflags -X main.releasesAPI=<github releases api url> at publish time,")
		fmt.Println("    or CHANDLER_RELEASES_API for this run)")
		return 0
	}
	resp, err := updateCheckHTTPClient.Get(api)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chandler: release feed unreachable: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "chandler: release feed returned %s\n", resp.Status)
		return 1
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		fmt.Fprintf(os.Stderr, "chandler: release feed is not understood: %v\n", err)
		return 1
	}
	latest := strings.TrimPrefix(strings.TrimSpace(payload.TagName), "v")
	switch compareVersions(latest, version) {
	case 1:
		fmt.Printf("chandler: a newer release is available: %s (this build: %s)\n", latest, version)
		return 2
	case -1:
		fmt.Printf("chandler: this build (%s) is newer than the latest release (%s).\n", version, latest)
		return 0
	default:
		fmt.Printf("chandler: up to date (%s).\n", version)
		return 0
	}
}

// The subcommand dispatch in front of the interactive default. The
// dispatch runs ahead of the config load and the prerequisite checks, so
// the subcommands never pay for the interactive front end.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "update":
			os.Exit(runUpdate())
		case "install":
			os.Exit(runInstallCmd())
		case "tool":
			os.Exit(runToolCmd(os.Args[2:]))
		case "doctor":
			os.Exit(runDoctor(os.Args[2:]))
		case "update-check":
			os.Exit(runUpdateCheck())
		case "uninstall":
			os.Exit(uninstall.Run())
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

	root := rootpin.Resolve()

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
	// the interactive path warns and continues instead of exiting, and
	// the names land in the menu's status line.
	prereqMissing := ensurePrerequisites()

	ask.PrintStackNotes()

	fmt.Println()

	// Ensure $LOCAL_BIN is on PATH (bash: after the notes, before the
	// menu), then create SCRIPT_TMPDIR.
	if err := pathsetup.EnsureLocalBinOnPath(); err != nil {
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
	if !rootpin.HasRegularFile(filepath.Join(root, "config.env")) {
		if _, err := ask.AskStackCloud(root, rd); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		fmt.Println()
	}

	// Best-effort keep the installed `chandler` command current at every
	// interactive launch (after the PATH block, before the menu): a
	// command deleted after an install is re-created here, a stale one
	// refreshed, and an already-current one costs two stat calls and
	// zero output. Warnings only — never an exit; the menu must come up
	// however the ensure fares.
	selfinstall.EnsureCommandOnStartup(os.Stdout, os.Stderr)

	// The full-screen TUI when the terminal can hold it, the plain numeric
	// prompt otherwise. scriptDir is the bash SCRIPT_DIR: the materialized
	// embedded scripts/ directory, where the install-<tool>.sh the actions
	// dispatch to live — the one wiring change the assets-embedding brings
	// to the menu itself. The menu remembers the last highlighted entry
	// across runs (menu.LoadLastSelection, saved on exit).
	scriptDir := filepath.Join(assetsDir, "scripts")
	if menu.UseTUI() {
		menu.TuiLoop(detectOS(), pkgMgr, root, scriptDir, prereqMissing, rd, menu.LoadLastSelection(len(menu.Items)))
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
