// Package home resolves the user's home directory the way the bash
// script's $HOME does: the HOME environment variable when set, falling
// back to the OS-level lookup (os.UserHomeDir — %USERPROFILE% on windows,
// where HOME is not conventionally set outside Git Bash / WSL). Every
// path bash spelled $HOME — LOCAL_BIN, the data root, the deployment-root
// default, the root-pin file, the installed `chandler` command — resolves
// through here, so the same binary behaves on unix and windows.
package home

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func Dir() string {
	if dir := os.Getenv("HOME"); dir != "" {
		return dir
	}
	if dir, err := os.UserHomeDir(); err == nil {
		return dir
	}
	return ""
}

func LocalBin() string {
	return filepath.Join(Dir(), ".local", "bin")
}

func ShareRoot() string {
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		return filepath.Join(data, "chandler")
	}
	return filepath.Join(Dir(), ".local", "share", "chandler")
}

func ScriptsDir() string {
	return filepath.Join(ShareRoot(), "scripts")
}

// DocumentsDir resolves the user's Documents directory. On windows the
// shell/profile code must follow the same redirection Explorer does — a
// OneDrive-redirected Documents is where PowerShell actually looks for
// its profiles — so the real path is asked of the OS through
// powershell.exe's [Environment]::GetFolderPath('MyDocuments') (always
// present on windows), with ~/Documents as the fallback whenever the
// probe fails. On every other platform this is ~/Documents, unchanged.
func DocumentsDir() string {
	docs := filepath.Join(Dir(), "Documents")
	if runtime.GOOS != "windows" {
		return docs
	}
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive",
		"-Command", "[Environment]::GetFolderPath('MyDocuments')").Output()
	if err != nil {
		return docs
	}
	resolved := strings.TrimSpace(string(out))
	if resolved == "" || !filepath.IsAbs(resolved) {
		return docs
	}
	return resolved
}
