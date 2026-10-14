package customsync

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"chandler/internal/home"
)

const EnvSource = "CHANDLER_CUSTOM_SCRIPTS"

const dsStore = ".DS_Store"

func ResolveSource(exeDir, root string) (string, bool) {
	candidates := make([]string, 0, 3)
	if env := os.Getenv(EnvSource); env != "" {
		candidates = append(candidates, env)
	}
	if exeDir != "" {
		candidates = append(candidates, filepath.Join(exeDir, "custom-scripts"))
	}
	if root != "" {
		candidates = append(candidates, filepath.Join(root, "GO", "assets", "custom-scripts"))
	}
	for _, dir := range candidates {
		if isDir(dir) {
			return dir, true
		}
	}
	return "", false
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func MigrateLegacy() (bool, error) {
	oldDir := filepath.Join(home.LocalBin(), "scripts")
	newDir := home.ScriptsDir()
	if !isDir(oldDir) {
		return false, nil
	}
	if _, err := os.Stat(newDir); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		return false, err
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return false, err
	}
	repointAwsConfig(oldDir, newDir)
	return true, nil
}

func repointAwsConfig(oldDir, newDir string) {
	path := filepath.Join(home.Dir(), ".aws", "config")
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return
	}
	target, err := os.Readlink(path)
	if err != nil || filepath.Clean(target) != filepath.Clean(filepath.Join(oldDir, "config")) {
		return
	}
	os.Remove(path)
	os.Symlink(filepath.Join(newDir, "config"), path)
}

func Sync(src, dst string) (changed int, report []string, err error) {
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			// The source root: only make sure dst exists, mirroring the
			// bash script's `mkdir -p "$DST"`.
			return os.MkdirAll(dst, 0o755)
		}
		if !d.IsDir() && d.Name() == dsStore {
			return nil // Finder metadata: never copied, never reported
		}

		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if isDir(target) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			changed++
			report = append(report, rel)
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}

		switch existing, err := os.ReadFile(target); {
		case err == nil && bytes.Equal(existing, data):
			return nil
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return err
		}

		perm := info.Mode().Perm()
		if err := os.WriteFile(target, data, perm); err != nil {
			return err
		}
		if err := os.Chmod(target, perm); err != nil {
			return err
		}
		changed++
		report = append(report, rel)
		return nil
	})
	return changed, report, err
}
