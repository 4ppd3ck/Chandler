// Package rootpin resolves the deployment root the way the bash script
// pins SCRIPT_DIR, extended so the binary works "from anywhere", and
// persists marker-validated roots to the root-pin file so the copy
// installed on the PATH keeps finding its checkout. The chain and the
// marker rules live here; main only calls Resolve.
package rootpin

import (
	"os"
	"path/filepath"
	"strings"

	"chandler/internal/home"
)

// ExeDir returns the directory of the running executable, with symlinks
// resolved (os.Executable + filepath.EvalSymlinks + filepath.Dir), or ""
// when the executable's path cannot be determined.
func ExeDir() string {
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

// Cwd returns the process's working directory, or "" when it cannot be
// determined (the chain then skips the cwd probes).
func Cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// HasRegularFile reports whether path exists as a regular file (bash's
// [[ -f ... ]], following symlinks).
func HasRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Evidence identifies which candidate of the root chain produced the
// deployment root. Resolve persists only the marker-validated filesystem
// candidates — the exe-dir and cwd ones — to the pin file.
type Evidence int

const (
	EvidenceEnv          Evidence = iota // CHANDLER_ROOT, verbatim
	EvidenceExeDir                       // marker beside the executable
	EvidenceExeDirParent                 // marker one level above it
	EvidencePin                          // the root-pin file's value
	EvidenceCwd                          // marker in the working directory
	EvidenceCwdParent                    // marker one level above it
	EvidenceDefault                      // $HOME/chandler fallback
)

func (e Evidence) String() string {
	switch e {
	case EvidenceEnv:
		return "CHANDLER_ROOT override"
	case EvidenceExeDir:
		return "marker beside the executable"
	case EvidenceExeDirParent:
		return "marker one level above the executable"
	case EvidencePin:
		return "root-pin file"
	case EvidenceCwd:
		return "marker in the working directory"
	case EvidenceCwdParent:
		return "marker one level above the working directory"
	default:
		return "default ($HOME/chandler)"
	}
}

// EnvRootPin overrides where the root-pin file lives; the file itself holds
// one absolute path — the deployment root an installed `chandler` copy
// resolves when nothing beside the binary or the cwd matches.
const EnvRootPin = "CHANDLER_ROOT_PIN"

// RootPinPath returns the root-pin file's location:
// $CHANDLER_ROOT_PIN when set, else
// ~/.config/chandler/path under the user's home (home.Dir).
func RootPinPath() string {
	if p := os.Getenv(EnvRootPin); p != "" {
		return p
	}
	return filepath.Join(home.Dir(), ".config", "chandler", "path")
}

// ReadRootPin returns the pinned root: the pin file's content, trimmed,
// and only when it is an absolute path. Anything else — a missing file,
// a read error, empty or relative content — reads as no pin (""); the
// candidate then just falls through the chain like any other
// non-matching one, so a stale pin never breaks resolution.
func ReadRootPin() string {
	data, err := os.ReadFile(RootPinPath())
	if err != nil {
		return ""
	}
	pin := strings.TrimSpace(string(data))
	if !filepath.IsAbs(pin) {
		return ""
	}
	return pin
}

// WriteRootPin best-effort persists root to the pin file: the write is
// skipped when the file already holds the same root, and every error —
// an unwritable directory, a failed write — is ignored silently. The
// pin is a convenience, never a promise: a stale or missing one only
// costs the fallback probes below it.
func WriteRootPin(root string) {
	path := RootPinPath()
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

// HasRootMarker reports whether dir carries the deployment-root marker,
// in either layout. The flat probe has one trap: in the nested layout the
// module directory <root>/GO also holds assets/ — and matching it there
// would mistake the module dir for the root (the exact bug the nested
// probe was introduced to avoid). So a bare assets/ tree counts only when
// the parent is not a nested checkout: if <dir>/../GO carries the nested
// marker, dir is that module directory and not a root.
func HasRootMarker(dir string) bool {
	if HasRegularFile(filepath.Join(dir, rootMarkerNested)) {
		return true
	}
	if HasRegularFile(filepath.Join(dir, rootMarker)) {
		parent := filepath.Join(dir, "..")
		return !HasRegularFile(filepath.Join(parent, rootMarkerNested))
	}
	return false
}

// Resolve determines the deployment root. The first match wins:
//
//  1. the CHANDLER_ROOT environment variable, verbatim;
//  2. the directory of the executable, when it looks like the checkout —
//     it must carry the root marker (see rootMarker) under it;
//  3. the parent of the executable's directory, same marker file — the
//     normal case: the binary lives inside the checkout (at its root or
//     in release/) and the checkout root one level up matches;
//  4. the pinned root from the pin file (RootPinPath), same marker
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
// best-effort persisted to the pin file (WriteRootPin), so the copy
// installed on the PATH keeps resolving this checkout however it is
// invoked.
func Resolve() string {
	root, evidence := ResolveFrom(
		os.Getenv("CHANDLER_ROOT"),
		ReadRootPin(),
		ExeDir(),
		Cwd(),
		home.Dir(),
	)
	switch evidence {
	case EvidenceExeDir, EvidenceExeDirParent, EvidenceCwd, EvidenceCwdParent:
		WriteRootPin(root)
	}
	return root
}

// ResolveFrom is Resolve's candidate chain. It probes envRoot,
// the marker file under exeDir and under its parent, pinValue, cwd and
// its parent, in that order, and falls back to $home/chandler.
// The returned evidence says which candidate won. Every candidate except
// envRoot and the default must carry the marker file
// (assets/scripts/install-kubectl.sh — see rootMarker); empty
// candidates are skipped, and pinValue only counts when it is absolute —
// the guarantee ReadRootPin makes for the real pin file.
func ResolveFrom(envRoot, pinValue, exeDir, cwd, home string) (string, Evidence) {
	if envRoot != "" {
		return envRoot, EvidenceEnv
	}
	if exeDir != "" {
		if HasRootMarker(exeDir) {
			return exeDir, EvidenceExeDir
		}
		// The binary usually sits inside the checkout at
		// <repo-root>/GO/chandler, so the exe-dir probe above
		// fails and one level up — the repo root — carries the marker
		// file. filepath.Join and filepath.Dir both clean, so the probe
		// path and the returned parent are canonical.
		if HasRootMarker(filepath.Join(exeDir, "..")) {
			return filepath.Dir(exeDir), EvidenceExeDirParent
		}
	}
	if pinValue != "" && filepath.IsAbs(pinValue) && HasRootMarker(pinValue) {
		return pinValue, EvidencePin
	}
	if cwd != "" {
		if HasRootMarker(cwd) {
			return cwd, EvidenceCwd
		}
		if HasRootMarker(filepath.Join(cwd, "..")) {
			return filepath.Dir(cwd), EvidenceCwdParent
		}
	}
	return filepath.Join(home, "chandler"), EvidenceDefault
}
