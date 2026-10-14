// Bash resolution for running the installers. On unix "bash" from PATH is
// the whole story. On windows, plain "bash" is a trap: C:\Windows\System32
// ships a bash.exe that is the WSL launcher — the program then dies with a
// cryptic "WSL ... execvpe(/bin/bash) failed" when no Linux distro is set
// up, and even with one it could not run the Windows-path installers.
// The real bash is Git Bash (Git for Windows), which may live anywhere
// (including another drive) and is often not on the system PATH at all.

package menu

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var errBashNotFound = errors.New("bash not found — the installers are bash scripts. Install Git for Windows (https://git-scm.com/download/win), or point CHANDLER_BASH at a bash.exe, then re-run")

func bashCommand() (string, error) {
	if runtime.GOOS != "windows" {
		return "bash", nil
	}
	return bashCommandWindows()
}

func bashCommandWindows() (string, error) {
	if p := os.Getenv("CHANDLER_BASH"); p != "" {
		if isExecutableFile(p) {
			return p, nil
		}
		return "", fmt.Errorf("CHANDLER_BASH=%q is not a file", p)
	}

	if p, err := exec.LookPath("bash"); err == nil && !isWSLBashLauncher(p) {
		return p, nil
	}

	if git, err := exec.LookPath("git"); err == nil {
		dir := filepath.Dir(git)
		for range 4 {
			for _, cand := range []string{
				filepath.Join(dir, "bin", "bash.exe"),
				filepath.Join(dir, "usr", "bin", "bash.exe"),
			} {
				if isExecutableFile(cand) {
					return cand, nil
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	for _, root := range knownGitRoots() {
		for _, cand := range []string{
			filepath.Join(root, "bin", "bash.exe"),
			filepath.Join(root, "usr", "bin", "bash.exe"),
		} {
			if isExecutableFile(cand) {
				return cand, nil
			}
		}
	}

	return "", errBashNotFound
}

func knownGitRoots() []string {
	var roots []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData", "USERPROFILE"} {
		if base := os.Getenv(env); base != "" {
			roots = append(roots, filepath.Join(base, "Git"))
		}
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		roots = append(roots, filepath.Join(base, "Programs", "Git"))
	}
	if base := os.Getenv("USERPROFILE"); base != "" {
		roots = append(roots, filepath.Join(base, "scoop", "apps", "git", "current"))
	}
	return roots
}

func isWSLBashLauncher(path string) bool {
	clean := strings.ToLower(filepath.Clean(path))
	for _, env := range []string{"SystemRoot", "WINDIR"} {
		if win := os.Getenv(env); win != "" {
			if launcher := strings.ToLower(filepath.Join(win, "System32", "bash.exe")); clean == launcher {
				return true
			}
		}
	}
	return false
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
