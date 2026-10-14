// Command packager builds the release artifacts for this repo's app:
//
//   - a stripped, static cross-compiled binary for every supported
//     GOOS/GOARCH pair (release/<app>-<goos>-<goarch>[.exe]);
//   - a macOS .app bundle per darwin target, zipped with the icon,
//     Info.plist and a Terminal launcher
//     (release/<app>-darwin-<arch>-app.zip), plus a CLI tarball with
//     the bare binary, the icon and a README
//     (release/<app>-darwin-<arch>.tar.gz);
//   - a Linux tarball per linux target holding the binary, a .desktop
//     entry, the hicolor icon set and a per-user installer
//     (release/<app>-linux-<arch>.tar.gz);
//   - a Windows zip per windows target holding the .exe, the icon and a
//     README (release/<app>-windows-<arch>.zip);
//   - release/SHA256SUMS covering every artifact.
//
// The app name is never hardcoded: it is derived from the repo root's
// folder name (the directory holding go.mod, found by walking up from
// the working directory) and can be overridden with -name, so the same
// pipeline serves any checkout — artifacts follow the
// <APPNAME>-<OS>-<ARCH> convention.
//
// scripts/release.sh and scripts/release.bat are thin wrappers around
// it, so both drivers share one implementation and produce an identical
// artifact set on any host that has the Go toolchain.
//
//	go run ./tools/packager                 # every target
//	go run ./tools/packager linux           # only linux/* targets
//	go run ./tools/packager darwin/arm64    # one target
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type target struct{ goos, goarch string }

func (t target) String() string { return t.goos + "/" + t.goarch }

var allTargets = []target{
	{"darwin", "arm64"},
	{"darwin", "amd64"},
	{"linux", "arm"},
	{"linux", "arm64"},
	{"linux", "amd64"},
	{"windows", "amd64"},
	{"windows", "arm64"},
}

func main() {
	outDir := flag.String("out", "release", "output directory for release artifacts")
	iconDir := flag.String("icons", "icons", "directory holding the generated icon set")
	versionFlag := flag.String("version", "", "version stamp (default: git revision, else a timestamp)")
	nameFlag := flag.String("name", "", "app name for artifact files (default: the repo folder name)")
	flag.Parse()

	targets, err := resolveTargets(flag.Args())
	if err != nil {
		fatal(err)
	}
	app := *nameFlag
	if app == "" {
		app, err = detectAppName()
		if err != nil {
			fatal(err)
		}
	}
	version := *versionFlag
	if version == "" {
		version = detectVersion()
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal(err)
	}
	if err := clean(outDir, app); err != nil {
		fatal(err)
	}

	fmt.Printf("%s release: %d target(s), version %s -> %s/\n", app, len(targets), version, *outDir)
	for _, t := range targets {
		out := filepath.Join(*outDir, binaryName(app, t))
		fmt.Printf("  %-16s -> %s\n", t, out)
		if err := buildBinary(t, out, version); err != nil {
			fatal(fmt.Errorf("%s: %w", t, err))
		}
		switch t.goos {
		case "darwin":
			if err := packageDarwin(*outDir, *iconDir, app, t, out, version); err != nil {
				fatal(fmt.Errorf("%s: %w", t, err))
			}
		case "linux":
			if err := packageLinux(*outDir, *iconDir, app, t, out, version); err != nil {
				fatal(fmt.Errorf("%s: %w", t, err))
			}
		case "windows":
			if err := packageWindows(*outDir, *iconDir, app, t, out, version); err != nil {
				fatal(fmt.Errorf("%s: %w", t, err))
			}
		}
	}
	if err := writeChecksums(outDir); err != nil {
		fatal(err)
	}
	if err := list(outDir); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "packager: %v\n", err)
	os.Exit(1)
}

// detectAppName derives the app name from the repo root's folder name:
// walking up from the working directory stops at the first directory
// holding go.mod. -name overrides it.
func detectAppName() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Base(dir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found from %s upward; pass -name", dir)
		}
		dir = parent
	}
}

// display capitalizes the app name for user-facing labels (chandler ->
// Chandler).
func display(app string) string {
	if app == "" {
		return app
	}
	return strings.ToUpper(app[:1]) + app[1:]
}

// resolveTargets maps the positional filters to build targets: a full
// GOOS/GOARCH pair, a GOOS group name, or nothing at all (every target).
// An unknown name is an error rather than a silent no-op.
func resolveTargets(args []string) ([]target, error) {
	if len(args) == 0 {
		return allTargets, nil
	}
	var out []target
	seen := map[string]bool{}
	for _, want := range args {
		matched := 0
		for _, t := range allTargets {
			if t.String() == want || t.goos == want {
				if !seen[t.String()] {
					seen[t.String()] = true
					out = append(out, t)
				}
				matched++
			}
		}
		if matched == 0 {
			return nil, fmt.Errorf("unknown target %q; want one of %s or a GOOS group (darwin/linux/windows)", want, targetList())
		}
	}
	return out, nil
}

func targetList() string {
	names := make([]string, 0, len(allTargets))
	for _, t := range allTargets {
		names = append(names, t.String())
	}
	return strings.Join(names, ", ")
}

func binaryName(app string, t target) string {
	name := app + "-" + t.goos + "-" + t.goarch
	if t.goos == "windows" {
		name += ".exe"
	}
	return name
}

// detectVersion stamps the build the way release builds have always been
// stamped: the short git revision when building from a checkout, a UTC
// timestamp otherwise.
func detectVersion() string {
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		if rev := strings.TrimSpace(string(out)); rev != "" {
			return "git-" + rev
		}
	}
	return "build-" + time.Now().UTC().Format("20060102150405")
}

// clean removes only this tool's own previous artifacts from outDir.
func clean(outDir *string, app string) error {
	entries, err := os.ReadDir(*outDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		// <app>-* is this tool's output; the legacy prefixes are names the
		// artifacts shipped under earlier (devops- pre-rebrand, chandler-
		// before the name became dynamic), swept so a stale binary cannot
		// be mistaken for a current one.
		if name == "SHA256SUMS" || strings.HasPrefix(name, app+"-") || legacyPrefix(name) {
			if err := os.RemoveAll(filepath.Join(*outDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// legacyPrefix reports whether name came from an earlier naming scheme
// of this tool.
func legacyPrefix(name string) bool {
	for _, p := range []string{"devops-", "chandler-"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func buildBinary(t target, out, version string) error {
	ldflags := "-s -w -X main.version=" + version
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+t.goos, "GOARCH="+t.goarch)
	if t.goos == "linux" && t.goarch == "arm" {
		cmd.Env = append(cmd.Env, "GOARM=7")
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// ---- macOS .app bundle ----------------------------------------------------

// launcher generates the Contents/MacOS script of the .app bundle: app
// is a full-screen terminal UI, so the bundle hands the real binary in
// Contents/Resources to Terminal.app instead of running it headless (a
// TUI with no terminal attached has nowhere to draw).
func launcher(app string) string {
	return `#!/bin/sh
# ` + display(app) + `.app launcher.
#
# ` + app + ` is a full-screen terminal UI, so the bundle hands the real
# binary in Contents/Resources to Terminal.app instead of running it
# headless (a TUI with no terminal attached has nowhere to draw).
set -e
here="$(cd "$(dirname "$0")" && pwd -P)"
exec /usr/bin/open -a Terminal "$here/../Resources/` + app + `"
`
}

func packageDarwin(outDir, iconDir, app string, t target, binPath, version string) error {
	tmp, err := os.MkdirTemp("", "packager-app-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	bundle := filepath.Join(tmp, display(app)+".app")
	macOS := filepath.Join(bundle, "Contents", "MacOS")
	res := filepath.Join(bundle, "Contents", "Resources")
	for _, d := range []string{macOS, res} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	if err := writeFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(infoPlist(app, version)), 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(macOS, app), []byte(launcher(app)), 0o755); err != nil {
		return err
	}
	if err := copyFile(binPath, filepath.Join(res, app), 0o755); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(iconDir, app+".icns"), filepath.Join(res, app+".icns"), 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(res, "README.txt"), []byte(bundleReadme(app, t, version)), 0o644); err != nil {
		return err
	}

	zipPath := filepath.Join(outDir, app+"-darwin-"+t.goarch+"-app.zip")
	if err := zipDir(zipPath, tmp, func(rel string) fs.FileMode {
		if filepath.Base(rel) == app {
			return 0o755
		}
		return 0o644
	}); err != nil {
		return err
	}
	fmt.Printf("  %-16s -> %s\n", t, zipPath)

	// The CLI package next to the app bundle: the same binary the bundle
	// carries, plus the icon and a README, for `./<app> install`.
	bin, err := os.ReadFile(binPath)
	if err != nil {
		return err
	}
	icns, err := os.ReadFile(filepath.Join(iconDir, app+".icns"))
	if err != nil {
		return err
	}
	tarPath := filepath.Join(outDir, app+"-darwin-"+t.goarch+".tar.gz")
	if err := writeTarGz(tarPath, []tarEntry{
		{app, bin, 0o755},
		{app + ".icns", icns, 0o644},
		{"README", []byte(darwinReadme(app, t, version)), 0o644},
	}); err != nil {
		return err
	}
	fmt.Printf("  %-16s -> %s\n", t, tarPath)
	return nil
}

func darwinReadme(app string, t target, version string) string {
	return fmt.Sprintf(`%[1]s %[2]s (%[3]s)

  %[1]s            the TUI binary (%[1]s version reports the build)
  %[1]s.icns   the app icon, for a .app bundle or a dock alias
  README          this file

Quick start:

    ./%[1]s            open the interactive menu
    ./%[1]s install    copy it to ~/.local/bin/%[1]s

The drag-and-drop app bundle ships separately as
%[1]s-%[4]s-app.zip. On first run macOS may quarantine a
downloaded binary; clear it with:

    xattr -d com.apple.quarantine %[1]s
`, app, version, t, t.goarch)
}

func infoPlist(app, version string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>` + display(app) + `</string>
	<key>CFBundleDisplayName</key>
	<string>` + display(app) + `</string>
	<key>CFBundleIdentifier</key>
	<string>com.` + app + `.tui</string>
	<key>CFBundleExecutable</key>
	<string>` + app + `</string>
	<key>CFBundleIconFile</key>
	<string>` + app + `</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>` + version + `</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
`
}

func bundleReadme(app string, t target, version string) string {
	return fmt.Sprintf(`%[1]s %[2]s (%[3]s)

A full-screen terminal UI, so this bundle launches the real binary
(Contents/Resources/%[1]s) in Terminal.app; the bare binary is also
shipped as %[1]s-%[4]s in the release folder. On first run macOS may
quarantine the download - clear it with:

    xattr -dr com.apple.quarantine /Applications/`+display(app)+`.app
`, app, version, t, t.goarch)
}

// ---- Linux tarball -------------------------------------------------------

func packageLinux(outDir, iconDir, app string, t target, binPath, version string) error {
	bin, err := os.ReadFile(binPath)
	if err != nil {
		return err
	}
	entries := []tarEntry{
		{app, bin, 0o755},
		{app + ".desktop", []byte(desktopEntry(app)), 0o644},
		{"install.sh", []byte(linuxInstaller(app)), 0o755},
		{"README", []byte(linuxReadme(app, t, version)), 0o644},
	}

	pngDir := filepath.Join(iconDir, "png")
	dirEntries, err := os.ReadDir(pngDir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(dirEntries))
	for _, e := range dirEntries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".png") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		size := strings.TrimSuffix(n, ".png")
		data, err := os.ReadFile(filepath.Join(pngDir, n))
		if err != nil {
			return err
		}
		entries = append(entries, tarEntry{"icons/hicolor/" + size + "/apps/" + app + ".png", data, 0o644})
	}

	tarPath := filepath.Join(outDir, app+"-linux-"+t.goarch+".tar.gz")
	if err := writeTarGz(tarPath, entries); err != nil {
		return err
	}
	fmt.Printf("  %-16s -> %s\n", t, tarPath)
	return nil
}

func desktopEntry(app string) string {
	return `[Desktop Entry]
Type=Application
Name=` + display(app) + `
GenericName=DevOps Deployment Tool
Comment=Interactive deployment toolkit for Kubernetes and cloud tooling
Exec=` + app + `
Icon=` + app + `
Terminal=true
Categories=Development;System;ConsoleOnly;
Keywords=devops;kubernetes;cloud;terraform;deploy;tui;
`
}

func linuxInstaller(app string) string {
	return `#!/bin/sh
# Per-user install of ` + app + ` (no root required). Override the target
# prefix with PREFIX=/usr/local ./install.sh.
set -eu
here="$(cd "$(dirname "$0")" && pwd -P)"
prefix="${PREFIX:-$HOME/.local}"

install -d "$prefix/bin" "$prefix/share/applications"
install -m 0755 "$here/` + app + `" "$prefix/bin/` + app + `"
install -m 0644 "$here/` + app + `.desktop" "$prefix/share/applications/` + app + `.desktop"

for png in "$here"/icons/hicolor/*/apps/` + app + `.png; do
	[ -e "$png" ] || continue
	size="$(basename "$(dirname "$(dirname "$png")")")"
	install -d "$prefix/share/icons/hicolor/$size/apps"
	install -m 0644 "$png" "$prefix/share/icons/hicolor/$size/apps/` + app + `.png"
done

echo "Installed $prefix/bin/` + app + ` (run: ` + app + `)"
`
}

func linuxReadme(app string, t target, version string) string {
	return fmt.Sprintf(`%[1]s %[2]s (%[4]s/linux-%[5]s)

  %[1]s            the TUI binary (%[1]s %[2]s reports the build)
  %[1]s.desktop    desktop entry (Terminal=true; the UI is full screen)
  install.sh          per-user install into ~/.local (PREFIX to override)
  icons/hicolor/...   app icons, 16px through 512px

Quick start:  ./install.sh   &&   %[1]s
`, app, version, t, t.goos, t.goarch)
}

// ---- Windows zip ---------------------------------------------------------

// packageWindows ships the .exe, the icon and a README as a zip: the
// portable download for a platform whose binaries already carry the icon
// in their PE resources, so no bundle wrapper is needed.
func packageWindows(outDir, iconDir, app string, t target, binPath, version string) error {
	tmp, err := os.MkdirTemp("", "packager-win-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	if err := copyFile(binPath, filepath.Join(tmp, app+".exe"), 0o755); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(iconDir, app+".ico"), filepath.Join(tmp, app+".ico"), 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(tmp, "README.txt"), []byte(windowsReadme(app, t, version)), 0o644); err != nil {
		return err
	}

	zipPath := filepath.Join(outDir, app+"-windows-"+t.goarch+".zip")
	if err := zipDir(zipPath, tmp, func(rel string) fs.FileMode {
		if strings.HasSuffix(rel, ".exe") {
			return 0o755
		}
		return 0o644
	}); err != nil {
		return err
	}
	fmt.Printf("  %-16s -> %s\n", t, zipPath)
	return nil
}

func windowsReadme(app string, t target, version string) string {
	return fmt.Sprintf(`%[1]s %[2]s (%[3]s)

  %[1]s.exe    the TUI binary (%[1]s.exe version reports the build)
  %[1]s.ico    the app icon
  README.txt      this file

Quick start (PowerShell or cmd):

    .\%[1]s.exe            open the interactive menu
    .\%[1]s.exe install    copy it to %%USERPROFILE%%\.local\bin\%[1]s.exe

The install actions are bash scripts, so bash must be on the PATH (Git
Bash or WSL); Git for Windows is located automatically.
`, app, version, t)
}

// ---- shared helpers ------------------------------------------------------

// tarEntry is one regular file in a release tarball.
type tarEntry struct {
	name string
	data []byte
	mode int64
}

// writeTarGz writes entries as a gzipped tar at path, carrying the modes
// the entries declare: the on-disk mode cannot (these artifacts are built
// on any host, Windows included, where exec bits do not exist).
func writeTarGz(path string, entries []tarEntry) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	mod := time.Now()
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.name, Mode: e.mode, Size: int64(len(e.data)), ModTime: mod, Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		if _, err := tw.Write(e.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeFile(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

func copyFile(src, dst string, mode fs.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFile(dst, data, mode)
}

// zipDir archives srcDir into zipPath, storing the mode from modeFor so
// unix exec bits survive on a Windows host (where the on-disk file mode
// cannot carry them).
func zipDir(zipPath, srcDir string, modeFor func(string) fs.FileMode) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)

	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: rel, Method: zip.Deflate}
		hdr.SetMode(modeFor(rel))
		hdr.Modified = time.Now()
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

func writeChecksums(outDir *string) error {
	entries, err := os.ReadDir(*outDir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "SHA256SUMS" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		f, err := os.Open(filepath.Join(*outDir, n))
		if err != nil {
			return err
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h.Sum(nil)), n)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "SHA256SUMS"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %s/SHA256SUMS (%d files)\n", *outDir, len(names))
	return nil
}

func list(outDir *string) error {
	entries, err := os.ReadDir(*outDir)
	if err != nil {
		return err
	}
	fmt.Println()
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return err
		}
		fmt.Printf("  %10d  %s\n", info.Size(), e.Name())
	}
	return nil
}
