// Package assets embeds every runtime asset the port needs at build time:
// the bash installers (scripts/), the dotfiles (bash-files/) and the
// maintained helper scripts (custom-scripts/), all under GO/assets/. A
// freshly built binary therefore carries the whole deployment toolkit —
// `go build` is all it takes to ship a new version — and the repo folders
// are dev-time sources only.
//
// Extract materializes the embedded tree onto disk, because the installers
// stay bash and child processes need real files. Embedding loses the Unix
// exec bits, so Extract sets them again for every *.sh file.
package assets

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The three subdirectories ship whole; no dot- or underscore-prefixed
// names live under them, so the plain patterns cover every file. A
// missing directive here compiles cleanly but embeds NOTHING — the
// extract test below is the tripwire for that.
//
//go:embed scripts bash-files custom-scripts
var embedded embed.FS

const dsStore = ".DS_Store"

func FS() fs.FS { return embedded }
func Extract(dest string) (err error) {
	return extract(embedded, dest)
}

func extract(fsys fs.FS, dest string) (err error) {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == dsStore {
			return nil
		}
		target := filepath.Join(dest, filepath.FromSlash(path))
		if d.IsDir() {
			if path == "." {
				return nil // dest itself; created by the first file write
			}
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		perm := info.Mode().Perm()
		if strings.HasSuffix(d.Name(), ".sh") || perm&0o111 != 0 {
			perm = 0o755
		}
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(target, data, perm); err != nil {
			return err
		}
		return os.Chmod(target, perm)
	})
}
