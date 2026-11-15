// selection.go remembers the last highlighted menu entry across runs —
// one integer in ~/.config/chandler/menu, written on every action run
// and on menu exit, read at startup. A missing, stale or hand-edited
// file can never push the menu out of range: LoadLastSelection clamps.

package menu

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"chandler/internal/home"
)

func selectionPath() string {
	return filepath.Join(home.Dir(), ".config", "chandler", "menu")
}

// LoadLastSelection returns the remembered highlighted entry, clamped
// into [0, n-1]: zero when nothing (or nothing valid) is remembered.
func LoadLastSelection(n int) int {
	data, err := os.ReadFile(selectionPath())
	if err != nil {
		return 0
	}
	i, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || i < 0 || i >= n {
		return 0
	}
	return i
}

// SaveLastSelection best-effort persists the highlighted entry; every
// failure is ignored — the memory is a convenience, never a promise.
func SaveLastSelection(sel int) {
	path := selectionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(strconv.Itoa(sel)+"\n"), 0o644)
}
