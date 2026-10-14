// RunAction: the Go port of run_menu_action() and run_install() from
// the bash original. Unlike bash, the confirm prompt and the ✓/✗
// markers stay plain text — the colors.go palette paints the TUI chrome
// only, so this output reads the same under the TUI and the fallback.

package menu

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"chandler/assets"
	"chandler/internal/ask"
	"chandler/internal/customsync"
	"chandler/internal/home"
	"chandler/internal/term"
)

var errInstallScriptMissing = errors.New("install script not found")

func RunAction(action, root, scriptDir string, rd *term.Reader) error {
	var tools []string

	switch action {
	case "__all":
		tools = []string{"kubectl", "k9s", "helm", "terraform", "aws", "brave", "helium", "bashtools", "fzf-setup"}
	case "__core":
		tools = []string{"kubectl", "k9s", "helm", "terraform", "aws", "bashtools"}
	case "__config":
		ask.AskStackCloud(root, rd)
		return nil
	case "__update_scripts":
		return UpdateScripts()
	default:
		tools = []string{action}
	}

	if action == "__all" || action == "__core" {
		fmt.Printf("\n? This will install %d tools. Continue? [y/N] ", len(tools))
		line, _ := readLine(os.Stdin)
		if answer := strings.TrimSpace(line); answer != "y" && answer != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	var firstErr error
	for _, tool := range tools {
		if err := runInstall(tool, scriptDir); err == nil {
			fmt.Printf("✓ %s done\n", tool)
		} else {
			fmt.Printf("✗ %s FAILED\n", tool)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func runInstall(tool, scriptDir string) error {
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
	cmd := exec.Command(bashExe, script)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("bash not found on PATH (the installers are bash scripts) — install Git Bash or WSL, or add bash to the PATH, and re-run: %w", err)
		}
		return err
	}
	return nil
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
