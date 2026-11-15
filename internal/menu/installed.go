// installed.go probes which installer-backed menu entries are already
// present, so the menu can show what was installed before this run: a
// dim `· installed` marker per row and an installed count in the status
// line. The probes are cheap (exec.LookPath plus two file stats) and
// re-run after every action, so a row loses its marker the moment its
// tool lands. `chandler doctor` reports the same facts with versions.

package menu

import (
	"os"
	"os/exec"
	"path/filepath"

	"chandler/internal/home"
)

// installProbes maps every installer-backed menu action to its presence
// probe. LookPath probes cover the tools that land on the PATH; the two
// AppImage browsers ship as files in ~/.local/bin; the Bash-tools
// install's presence is its deployed scripts tree.
var installProbes = map[string]func() bool{
	"kubectl":   func() bool { return lookPathOK("kubectl") },
	"k9s":       func() bool { return lookPathOK("k9s") },
	"helm":      func() bool { return lookPathOK("helm") },
	"terraform": func() bool { return lookPathOK("terraform") },
	"aws":       func() bool { return lookPathOK("aws") },
	"oc":        func() bool { return lookPathOK("oc") },
	"brave":     func() bool { return localBinFile("brave.AppImage") },
	"helium":    func() bool { return localBinFile("helium.AppImage") },
	"bashtools": func() bool {
		info, err := os.Stat(home.ScriptsDir())
		return err == nil && info.IsDir()
	},
	"fzf-setup": func() bool { return lookPathOK("fzf") },
}

func lookPathOK(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func localBinFile(name string) bool {
	info, err := os.Stat(filepath.Join(home.LocalBin(), name))
	return err == nil && info.Mode().IsRegular()
}

// InstalledTools probes every installer-backed entry and returns
// action -> installed. All ten installer-backed actions are always
// present as keys, so the count denominator is len(map).
func InstalledTools() map[string]bool {
	m := make(map[string]bool, len(installProbes))
	for action, probe := range installProbes {
		m[action] = probe()
	}
	return m
}

// refreshInstalled re-probes into an existing map (the one the TUI's
// draw calls read), so the markers update after every action without
// the callers holding another reference.
func refreshInstalled(m map[string]bool) {
	fresh := InstalledTools()
	for k := range m {
		delete(m, k)
	}
	for k, v := range fresh {
		m[k] = v
	}
}
