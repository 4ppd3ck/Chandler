// RunAction: the Go port of run_menu_action() and run_install() from
// the bash original. Unlike bash, the confirm prompt and the ✓/✗
// markers stay plain text — the colors.go palette paints the TUI chrome
// only, so this output reads the same under the TUI and the fallback.

package menu

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"chandler/assets"
	"chandler/internal/ask"
	"chandler/internal/customsync"
	"chandler/internal/home"
	"chandler/internal/term"
)

var errInstallScriptMissing = errors.New("install script not found")

// RunAction runs one menu action — the interactive path (the TUI, the
// fallback menu), where the bulk-install confirm reads the terminal.
func RunAction(action, root, scriptDir string, rd *term.Reader) error {
	return runAction(action, root, scriptDir, false, nil, rd)
}

// RunTool runs one action for the `chandler tool` subcommand:
// assumeYes skips the bulk-install confirm (the -y flag) and extraArgs
// are forwarded to the installer script verbatim (a `--`-separated tail
// on the command line — e.g. a version, which the prompting installers
// accept as their first argument). extraArgs only ever reach a
// single-tool run.
func RunTool(action, root, scriptDir string, assumeYes bool, extraArgs []string, rd *term.Reader) error {
	return runAction(action, root, scriptDir, assumeYes, extraArgs, rd)
}

// KnownToolActions lists the actions `chandler tool` accepts: the two
// bulk actions plus every installer-backed menu entry, in menu order.
func KnownToolActions() []string {
	out := []string{"all", "core"}
	for _, item := range Items {
		switch item.Action {
		case "__all", "__core", "__config", "__update_scripts", "__uninstall":
			continue
		}
		out = append(out, item.Action)
	}
	return out
}

func runAction(action, root, scriptDir string, assumeYes bool, extraArgs []string, rd *term.Reader) error {
	switch action {
	case "__all":
		return runToolList([]string{"kubectl", "k9s", "helm", "terraform", "aws", "brave", "helium", "bashtools", "fzf-setup"}, assumeYes, root, scriptDir)
	case "__core":
		return runToolList([]string{"kubectl", "k9s", "helm", "terraform", "aws", "bashtools"}, assumeYes, root, scriptDir)
	case "__config":
		ask.AskStackCloud(root, rd)
		return nil
	case "__update_scripts":
		return UpdateScripts()
	case "__uninstall":
		fmt.Printf("\n? This removes the installed command, the deployed scripts tree, the desktop entry and every config line chandler wrote. Continue? [y/N] ")
		line, _ := readLine(os.Stdin)
		if answer := strings.TrimSpace(line); answer != "y" && answer != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
		return runSelfUninstall()
	default:
		return runToolList([]string{action}, assumeYes, root, scriptDir, extraArgs...)
	}
}

// runToolList runs installer actions in order — one confirm for the bulk
// lists (skipped by assumeYes), one log per tool, one ✓/✗ summary for
// multi-tool runs. The menu's multi-select (space-checked entries) and
// the bulk menu actions both land here, so a hand-picked set behaves
// exactly like "Install everything". extraArgs are forwarded to every
// script but only ever arrive for a single-tool run.
func runToolList(tools []string, assumeYes bool, root, scriptDir string, extraArgs ...string) error {
	if len(tools) > 1 {
		if assumeYes {
			fmt.Printf("Installing %d tools (--yes).\n", len(tools))
		} else {
			fmt.Printf("\n? This will install %d tools. Continue? [y/N] ", len(tools))
			line, _ := readLine(os.Stdin)
			if answer := strings.TrimSpace(line); answer != "y" && answer != "Y" {
				fmt.Println("Aborted.")
				return nil
			}
		}
	}

	// Every child run tees its output into <root>/tmp/logs/<tool>-<ts>.log
	// — a failure's evidence survives the scrollback, which the bulk runs
	// especially need.
	logDir := filepath.Join(root, "tmp", "logs")

	var firstErr error
	results := make([]installResult, 0, len(tools))
	for _, tool := range tools {
		start := time.Now()
		err := runInstall(tool, scriptDir, logDir, extraArgs...)
		results = append(results, installResult{tool: tool, failed: err != nil, dur: time.Since(start)})
		if err == nil {
			// The Bash-tools installer wires aliases and the k/aw/tf
			// dispatchers pointing into the deployed scripts tree, but it
			// does not populate the tree itself — without a sync here,
			// every alias and the tui_select picker would be dead until
			// the first `chandler update`. Syncing is the same
			// add-new/overwrite-differing/never-delete pass `update` runs.
			if tool == "bashtools" {
				if uerr := UpdateScripts(); uerr != nil {
					fmt.Printf("   warning: script sync after bashtools failed: %v\n", uerr)
				}
			}
			fmt.Printf("✓ %s done\n", tool)
		} else {
			fmt.Printf("✗ %s FAILED\n", tool)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if len(tools) > 1 {
		printSummary(results)
	}
	return firstErr
}

// installResult is one line of the bulk-run summary.
type installResult struct {
	tool   string
	failed bool
	dur    time.Duration
}

// printSummary reports the bulk runs' ✓/✗ table with per-tool elapsed
// time. A single-tool run is its own line and skips the table.
func printSummary(results []installResult) {
	var okCount int
	var total time.Duration
	fmt.Println("\nSummary:")
	for _, r := range results {
		total += r.dur
		mark, state := "✓", "ok"
		if r.failed {
			mark, state = "✗", "FAILED"
		} else {
			okCount++
		}
		fmt.Printf("  %s %-12s %-7s %s\n", mark, r.tool, state, r.dur.Round(100*time.Millisecond))
	}
	fmt.Printf("  %d/%d succeeded · total %s\n", okCount, len(results), total.Round(100*time.Millisecond))
}

func runInstall(tool, scriptDir, logDir string, extraArgs ...string) error {
	script := filepath.Join(scriptDir, "install-"+tool+".sh")
	if info, err := os.Stat(script); err != nil || !info.Mode().IsRegular() {
		fmt.Printf("ERROR: install script for '%s' not found (%s).\n", tool, script)
		return errInstallScriptMissing
	}
	fmt.Printf(">>> Installing %s...\n", tool)
	bashExe, err := bashCommand()
	if err != nil {
		return err
	}
	cmd := exec.Command(bashExe, append([]string{script}, extraArgs...)...)
	cmd.Stdin = os.Stdin // the installers' own prompts stay interactive
	// Tee the child's output into a per-run log, then keep forwarding to
	// the real stdout/stderr: stdin is untouched, and the interactive
	// pickers draw on /dev/tty, so the piped stdout costs the scripts
	// nothing but colors. Failures name the log; best-effort — a log
	// directory that cannot be created never blocks an install.
	var logPath string
	if logDir != "" {
		if err := os.MkdirAll(logDir, 0o755); err == nil {
			logPath = filepath.Join(logDir, fmt.Sprintf("%s-%s.log", tool, time.Now().Format("20060102-150405")))
			if f, ferr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); ferr == nil {
				defer f.Close()
				fmt.Fprintf(f, "chandler install log: %s — %s\n\n", tool, time.Now().Format(time.RFC3339))
				cmd.Stdout = io.MultiWriter(os.Stdout, f)
				cmd.Stderr = io.MultiWriter(os.Stderr, f)
			}
		}
	}
	if cmd.Stdout == nil {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("bash not found on PATH (the installers are bash scripts) — install Git Bash or WSL, or add bash to the PATH, and re-run: %w", err)
		}
		if logPath != "" {
			fmt.Printf("   (output logged to %s)\n", logPath)
		}
		return err
	}
	return nil
}

// runSelfUninstall runs the `uninstall` subcommand of the running binary
// as a subprocess — exactly the code path of `chandler uninstall`, in a
// fresh process with stdio inherited, so the removal happens under the
// same guarantees as the subcommand (best-effort, the running image kept
// on windows by renaming it aside) and the menu's own state cannot
// interfere with it.
func runSelfUninstall() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "uninstall")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func UpdateScripts() error {
	tmp, err := os.MkdirTemp("", "chandler-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := assets.Extract(tmp); err != nil {
		return err
	}

	if moved, err := customsync.MigrateLegacy(); err != nil {
		return err
	} else if moved {
		fmt.Printf("Moved the scripts tree to its new home: %s\n", home.ScriptsDir())
	}

	dst := home.ScriptsDir()
	fmt.Printf("\nSyncing custom scripts from embedded scripts (build %s) → %s\n", buildLabel(), dst)
	changed, report, err := customsync.Sync(filepath.Join(tmp, "custom-scripts"), dst)
	if err != nil {
		return err
	}
	for _, rel := range report {
		fmt.Printf("  %s\n", rel)
	}
	if changed == 0 {
		fmt.Println("Custom scripts: already up to date.")
	} else {
		fmt.Printf("Custom scripts: %d item(s) updated.\n", changed)
	}
	return nil
}

func buildLabel() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "dev"
}

func readLine(f *os.File) (string, error) {
	var line []byte
	var buf [1]byte
	for {
		n, err := f.Read(buf[:])
		if n == 1 {
			if buf[0] == '\n' {
				return string(line), nil
			}
			line = append(line, buf[0])
			continue
		}
		if err != nil {
			return string(line), err
		}
	}
}
