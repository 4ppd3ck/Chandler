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
	"path/filepath"
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
