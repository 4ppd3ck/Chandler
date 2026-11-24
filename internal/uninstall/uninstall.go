// Package uninstall is `chandler uninstall`: install and update in
// reverse, so "remove chandler" is one command — or, read as the manual
// recipe, two rm's and a config cleanup. Everything the app ever deployed
// under the home directory goes; shared tools and the user's credentials
// never do. The exit code is 1 only when a removal genuinely failed;
// missing things, skipped strips and the windows running-image case are
// reported, not failed — uninstall is best-effort by design, since a
// half-broken install must still be removable.
package uninstall

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"chandler/internal/home"
	"chandler/internal/pathsetup"
	"chandler/internal/selfinstall"
)

// hasRegularFile reports whether path exists as a regular file (bash's
// [[ -f ... ]], following symlinks).
func hasRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Run performs the uninstall and returns the process exit code. No
// resolveRoot here — its side effect re-writes the root-pin file, and
// uninstall is about to delete that directory; nothing below needs the
// deployment root, only the home-relative paths.
func Run() int {
	target := selfinstall.InstallTarget()
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
	selfinstall.CleanupStaleOld(target)

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
	pinDir := selfinstall.RootPinDir()
	switch err := removeTarget(pinDir); {
	case err == nil:
		fmt.Printf("removed %s\n", pinDir)
	case errors.Is(err, fs.ErrNotExist):
	default:
		fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", pinDir, err)
	}

	// The desktop entry and hicolor icons the Linux tarball installs —
	// chandler-named files in the user's own share tree.
	desktop := filepath.Join(home.Dir(), ".local", "share", "applications", selfinstall.CommandName()+".desktop")
	if err := removeTarget(desktop); err == nil {
		fmt.Printf("removed %s\n", desktop)
	}
	icons, _ := filepath.Glob(filepath.Join(home.Dir(), ".local", "share", "icons", "hicolor", "*", "apps", selfinstall.CommandName()+".png"))
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

	// The shell configs: strip every chandler marker block of the family
	// isChandlerBlockBegin/End recognizes and every bare legacy PATH line,
	// best-effort with warnings — a config that cannot be rewritten still
	// leaves the rest of the uninstall done. This runs after the restores
	// above so a backup carrying an old block is cleaned too. config.fish
	// joins the list: fish's PATH block is a marker block like the rest.
	for _, name := range []string{".bashrc", ".zshrc", ".bash_profile"} {
		configFile := filepath.Join(home.Dir(), name)
		if stripped := StripChandlerLines(configFile, localBin); stripped {
			fmt.Printf("cleaned %s\n", configFile)
		}
	}
	if stripped := StripChandlerLines(pathsetup.FishConfigPath(), localBin); stripped {
		fmt.Printf("cleaned %s\n", pathsetup.FishConfigPath())
	}
	if runtime.GOOS == "windows" {
		docs := home.DocumentsDir()
		for _, dir := range []string{"PowerShell", "WindowsPowerShell"} {
			profile := filepath.Join(docs, dir, "Microsoft.PowerShell_profile.ps1")
			if stripped := StripChandlerLines(profile, localBin); stripped {
				fmt.Printf("cleaned %s\n", profile)
			}
		}
		// The user-scope registry PATH entry cmd.exe reads — the write the
		// PATH setup made through PowerShell — is reversed the same way.
		if err := removeCmdUserPathEntry(localBin); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove the user PATH entry for cmd.exe: %v\n", err)
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

// isChandlerBlockBegin and isChandlerBlockEnd recognize the whole chandler
// marker-block family by prefix and suffix — `# >>> chandler >>>` (the PATH
// block), `# >>> chandler shell tools >>>` (the zsh sourcing block the
// Bash-tools installer appends) and `# >>> chandler fzf/cursor setup >>>`
// (the opt-in fzf/cursor block) — so uninstall strips every block kind,
// not only the exact PATH markers.
func isChandlerBlockBegin(line string) bool {
	return strings.HasPrefix(line, "# >>> chandler") && strings.HasSuffix(line, ">>>")
}

func isChandlerBlockEnd(line string) bool {
	return strings.HasPrefix(line, "# <<< chandler") && strings.HasSuffix(line, "<<<")
}

// StripChandlerLines rewrites configFile without chandler's additions:
// every chandler marker block of the family isChandlerBlockBegin/End
// recognizes (markers included) and every bare legacy PATH line naming
// localBin — the bash export and the PowerShell $env:Path assignment from
// installs before the marker blocks. Comparisons are on trimmed lines, so
// CRLF endings match too. The file is left untouched (false returned) when
// it is unreadable or holds nothing of ours; a strip that leaves it empty
// still writes, because an empty config is the honest result.
func StripChandlerLines(configFile, localBin string) bool {
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
		case isChandlerBlockBegin(trimmed):
			inBlock = true
			stripped = true
			continue
		case isChandlerBlockEnd(trimmed):
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

// cmdUserPathRemoveScript drops the folder from the user-scope PATH when
// an entry matches it (trimmed, case-insensitive), and reports
// removed/absent — the exact reverse of the PATH setup's ensure script.
// An entry that only holds whitespace goes too; an empty result deletes
// the value outright instead of lingering as an empty string.
const cmdUserPathRemoveScript = `
$bin = $env:CHANDLER_LOCAL_BIN
if ([string]::IsNullOrEmpty($bin)) { exit 1 }
$p = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($null -eq $p) { Write-Output 'absent'; exit 0 }
$kept = @($p -split ';' | Where-Object { $_.Trim() -ne '' -and $_.Trim() -ine $bin })
$rest = $kept -join ';'
if ($rest -ceq $p.TrimEnd(';')) { Write-Output 'absent'; exit 0 }
if ($rest -eq '') {
    [Environment]::SetEnvironmentVariable('Path', $null, 'User')
} else {
    [Environment]::SetEnvironmentVariable('Path', $rest, 'User')
}
Write-Output 'removed'
`

// removeCmdUserPathEntry reverses the cmd.exe user-PATH automation: the
// HKCU\Environment\Path value loses the LOCAL_BIN entry, through the
// same powershell.exe -NoProfile route (and WM_SETTINGCHANGE broadcast)
// the write used. Best-effort — a missing powershell.exe or a failed run
// is the caller's warning, never an uninstall failure.
func removeCmdUserPathEntry(localBin string) error {
	_, err := pathsetup.RunUserPathScript(cmdUserPathRemoveScript, localBin)
	return err
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
