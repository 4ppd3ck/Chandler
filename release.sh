#!/bin/sh
# Assemble chandler releases.
#
#   release.sh                            every target
#   release.sh linux                      only linux/* targets
#   release.sh darwin/arm64               one target
#   release.sh freebsd                    freebsd/* targets
#
# A self-contained pipeline, plain sh end to end: no Go helper tool — the
# script cross-compiles every target, packages it per platform, and writes
# release/SHA256SUMS. The same script therefore drives macOS, Linux and
# Git Bash hosts (release.bat shells out to it), and a target
# failed build aborts the run before its artifacts can ride along.
#
# Every package is a zip — one archive format for all platforms, and the
# one both PowerShell (Git Bash hosts) and every desktop OS extract natively.
#
# Output in release/ (the artifact set the README documents) — every
# package carries its platform's binary inside, so no bare binaries land
# beside their own packages:
#
#   <app>-darwin-<arch>.zip        Chandler.app: icon, Info.plist, and a
#                                  launcher that hands the full-screen TUI
#                                  to Terminal.app (the binary sits in
#                                  Contents/Resources/ — CLI use runs it
#                                  directly from there)
#   <app>-linux-<arch>.zip         binary + .desktop entry + per-user
#                                  install.sh + the hicolor icon set
#   <app>-freebsd-<arch>.zip       the same desktop bundle as linux
#   <app>-windows-<arch>.zip       exe + .ico + README (the exe itself
#                                  carries the icon via the committed
#                                  rsrc_windows_*.syso resources)
#   SHA256SUMS                     every artifact above
#
# Windows binaries embed the icon at build time; darwin, linux and freebsd
# packaging reads the icon set from icons/. Nothing is code-signed — the
# README's unsigned-artifact note applies.
set -e
cd "$(dirname "$0")"

# The app name is never hardcoded: walk up from this script to the
# directory holding go.mod (the checkout root here, a GO/ parent in the
# nested layout) and take its folder name, lowercased, so every checkout —
# a local chandler/, a CI clone Chandler/ — yields the same artifact names.
MODROOT=""
dir="$(pwd)"
while [ "$dir" != "/" ]; do
    if [ -f "$dir/go.mod" ]; then
        MODROOT="$dir"
        break
    fi
    dir=$(dirname "$dir")
done
if [ -z "$MODROOT" ]; then
    echo "error: no go.mod found from $(pwd) upward" >&2
    exit 1
fi
cd "$MODROOT"
APP=$(basename "$MODROOT" | tr '[:upper:]' '[:lower:]')
DISPLAY=$(printf '%s' "$APP" | awk '{print toupper(substr($0,1,1)) substr($0,2)}')
ICONDIR="$MODROOT/icons"
OUT="$MODROOT/release"

if ! command -v go >/dev/null 2>&1; then
    echo "error: Go toolchain is required" >&2
    exit 1
fi
for need in "$ICONDIR/$APP.icns" "$ICONDIR/$APP.ico" "$ICONDIR/png"; do
    if [ ! -e "$need" ]; then
        echo "error: $need missing — the darwin/windows/linux packaging embeds it" >&2
        exit 1
    fi
done

mkdir -p "$OUT"
# Sweep this script's own artifacts from earlier runs (plus the legacy
# devops- prefix from before the rebrand) so stale files cannot ride along
# in the new SHA256SUMS.
rm -rf "$OUT/$APP-"* "$OUT/chandler-"* "$OUT/devops-"* "$OUT/SHA256SUMS"

# The version stamps every build (`chandler version` reports it): the short
# git revision when building from a checkout, a UTC timestamp otherwise.
VERSION=$(git rev-parse --short HEAD 2>/dev/null | sed 's/^/git-/')
[ -n "$VERSION" ] || VERSION="build-$(date -u +%Y%m%d%H%M%S)"

# darwin/arm64 darwin/amd64 linux/arm linux/arm64 linux/amd64
# linux/riscv64 freebsd/amd64 windows/amd64 windows/arm64 — the supported
# platform matrix (riscv64: RISC-V SBCs and QEMU; freebsd: NAS/homelab
# boxes — both pure CGO_ENABLED=0 cross-compiles).
ALL_TARGETS="darwin/arm64 darwin/amd64 linux/arm linux/arm64 linux/amd64 linux/riscv64 freebsd/amd64 windows/amd64 windows/arm64"
TARGETS=""
for arg in "$@"; do
    case "$arg" in
        darwin|linux|freebsd|windows)
            matched=""
            for t in $ALL_TARGETS; do
                case "$t" in
                    "$arg"/*) matched="$matched $t" ;;
                esac
            done
            if [ -z "$matched" ]; then
                echo "error: no targets for \"$arg\"" >&2
                exit 1
            fi
            TARGETS="$TARGETS $matched"
            ;;
        darwin/*|linux/*|freebsd/*|windows/*)
            case " $ALL_TARGETS " in
                *" $arg "*) TARGETS="$TARGETS $arg" ;;
                *) echo "error: unknown target \"$arg\"; want one of:$ALL_TARGETS" >&2; exit 1 ;;
            esac
            ;;
        *)
            echo "error: unknown target \"$arg\"; want a GOOS group (darwin/linux/freebsd/windows) or an os/arch pair:$ALL_TARGETS" >&2
            exit 1
            ;;
    esac
done
if [ -z "$TARGETS" ]; then
    TARGETS=" $ALL_TARGETS"
fi
# A repeated argument must not build (or sweep-sweep) twice.
DEDUPED=""
for t in $TARGETS; do
    case " $DEDUPED " in
        *" $t "*) ;;
        *) DEDUPED="$DEDUPED $t" ;;
    esac
done
TARGETS=$DEDUPED

LDFLAGS="-s -w -X main.version=$VERSION"
ERRFILE=$(mktemp)
STAGE=""
BINDIR=""
cleanup() {
    [ -z "$STAGE" ] || rm -rf "$STAGE"
    [ -z "$BINDIR" ] || rm -rf "$BINDIR"
    rm -f "$ERRFILE"
}
trap cleanup EXIT

# build <os> <arch> <goarm> <outfile> — stripped, static, trimmed; windows
# binaries pick up the icon from the committed rsrc_windows_*.syso.
build() {
    os=$1; arch=$2; goarm=$3; bout=$4
    if [ -n "$goarm" ]; then
        GOOS=$os GOARCH=$arch GOARM=$goarm CGO_ENABLED=0 \
            go build -trimpath -ldflags "$LDFLAGS" -o "$bout" .
    else
        GOOS=$os GOARCH=$arch CGO_ENABLED=0 \
            go build -trimpath -ldflags "$LDFLAGS" -o "$bout" .
    fi
}

# render <in> <out> — expand the @NAME@ placeholders the generated files
# use into this run's app name, display name, version and target label.
# Writes through a sibling temp so an in-place call (src == dst — every
# heredoc lands on its final path first) cannot truncate its own input.
RTARGET=""; RARCH=""; RGOOS=""
render() {
    sed -e "s|@APP@|$APP|g" \
        -e "s|@DISPLAY@|$DISPLAY|g" \
        -e "s|@VERSION@|$VERSION|g" \
        -e "s|@TARGET@|$RTARGET|g" \
        -e "s|@ARCH@|$RARCH|g" \
        -e "s|@GOOS@|$RGOOS|g" \
        "$1" > "$1.rend"
    mv "$1.rend" "$2"
}

# zip_dir_unix <stage-dir> <name-without-ext> <entries...> — every
# unix-content package: the darwin app bundle, the linux
# and freebsd desktop bundles. The stage's on-disk modes ride the zip's
# external attributes; every file is chmod'ed explicitly first, so the
# exec bits survive extraction on any unix host. zip is required,
# deliberately without a fallback: the contents carry unix exec bits that
# must survive extraction, and the one zip writer a Windows host has
# (PowerShell's Compress-Archive) stores no unix permissions — a
# Compress-Archive'd bundle would extract non-executable.
zip_dir_unix() {
    stage=$1; name=$2; shift 2
    if ! command -v zip >/dev/null 2>&1; then
        echo "error: cannot write $name.zip — install zip (e.g. apt install zip)" >&2
        exit 1
    fi
    (cd "$stage" && zip -r -q "$OUT/$name.zip" "$@")
    echo "   + $name.zip"
}

# zip_dir_win <stage-dir> <name-without-ext> — the Windows distribution
# zip. Nothing inside needs a unix exec bit, so on a Git Bash host (which
# ships no zip.exe) PowerShell's Compress-Archive is an acceptable
# fallback; paths travel via env vars so no shell quoting reaches
# PowerShell, and cygpath converts /c/... to C:\...
zip_dir_win() {
    stage=$1; name=$2
    if command -v zip >/dev/null 2>&1; then
        (cd "$stage" && zip -q -r "$OUT/$name.zip" .)
    elif command -v powershell.exe >/dev/null 2>&1; then
        ZIP_STAGE="$(cygpath -w "$stage" 2>/dev/null || printf '%s' "$stage")" \
        ZIP_DST="$(cygpath -w "$OUT/$name.zip" 2>/dev/null || printf '%s' "$OUT/$name.zip")" \
        powershell.exe -NoProfile -Command 'Compress-Archive -Path (Join-Path $env:ZIP_STAGE "*") -DestinationPath $env:ZIP_DST -Force'
    else
        echo "error: cannot write $name.zip — install zip (e.g. apt install zip) or run on Windows with PowerShell" >&2
        exit 1
    fi
    echo "   + $name.zip"
}

# make_darwin <arch> <bin> — the drag-and-drop Chandler.app, the darwin
# platform's only package (Finder treats a bare executable as an unknown
# document and shows no icon). The bundle launches Contents/Resources/<app>
# through Terminal.app: a full-screen TUI has nowhere to draw without a
# terminal; CLI use runs that same binary directly from inside the bundle.
# Ad-hoc signed where codesign exists.
make_darwin() {
    arch=$1; bin=$2
    RTARGET="darwin/$arch"; RARCH=$arch; RGOOS=darwin

    STAGE=$(mktemp -d)
    bundle="$STAGE/$DISPLAY.app"
    mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources"

    cat > "$bundle/Contents/MacOS/$APP" <<'LAUNCHER'
#!/bin/sh
# @DISPLAY@.app launcher.
#
# @APP@ is a full-screen terminal UI, so the bundle hands the real
# binary in Contents/Resources to Terminal.app instead of running it
# headless (a TUI with no terminal attached has nowhere to draw).
set -e
here="$(cd "$(dirname "$0")" && pwd -P)"
exec /usr/bin/open -a Terminal "$here/../Resources/@APP@"
LAUNCHER
    render "$bundle/Contents/MacOS/$APP" "$bundle/Contents/MacOS/$APP"
    chmod 0755 "$bundle/Contents/MacOS/$APP"

    cp "$bin" "$bundle/Contents/Resources/$APP"
    chmod 0755 "$bundle/Contents/Resources/$APP"
    # The repo's icon files ship 0755; the packages carry them 0644, like
    # every other data file.
    cp "$ICONDIR/$APP.icns" "$bundle/Contents/Resources/$APP.icns"
    chmod 0644 "$bundle/Contents/Resources/$APP.icns"

    cat > "$bundle/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>@DISPLAY@</string>
	<key>CFBundleDisplayName</key>
	<string>@DISPLAY@</string>
	<key>CFBundleIdentifier</key>
	<string>com.@APP@.tui</string>
	<key>CFBundleExecutable</key>
	<string>@APP@</string>
	<key>CFBundleIconFile</key>
	<string>@APP@</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>@VERSION@</string>
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
PLIST
    render "$bundle/Contents/Info.plist" "$bundle/Contents/Info.plist"

    cat > "$bundle/Contents/Resources/README.txt" <<'BREADME'
@APP@ @VERSION@ (@TARGET@)

A full-screen terminal UI, so this bundle launches the real binary
(Contents/Resources/@APP@) in Terminal.app. For plain CLI use, run that
binary directly from inside the bundle. On first run macOS may
quarantine the download - clear it with:

    xattr -dr com.apple.quarantine /Applications/@DISPLAY@.app
BREADME
    render "$bundle/Contents/Resources/README.txt" "$bundle/Contents/Resources/README.txt"

    if command -v codesign >/dev/null 2>&1; then
        codesign --force --sign - "$bundle" >/dev/null 2>&1 || true
    fi
    zip_dir_unix "$STAGE" "$APP-darwin-$arch" "$DISPLAY.app"
    rm -rf "$STAGE"; STAGE=""
}

# make_unix_bundle <os> <arch> <bin> — ELF executables carry no icon, so
# the linux and freebsd zips ship the XDG desktop entry, the per-user
# install.sh and the hicolor icon set beside the binary (XDG desktops on
# both platforms read the same layout).
make_unix_bundle() {
    os=$1; arch=$2; bin=$3
    RTARGET="$os/$arch"; RARCH=$arch; RGOOS=$os

    STAGE=$(mktemp -d)
    cp "$bin" "$STAGE/$APP"
    chmod 0755 "$STAGE/$APP"

    cat > "$STAGE/$APP.desktop" <<'DESKTOP'
[Desktop Entry]
Type=Application
Name=@DISPLAY@
GenericName=DevOps Deployment Tool
Comment=Interactive deployment toolkit for Kubernetes and cloud tooling
Exec=@APP@
Icon=@APP@
Terminal=true
Categories=Development;System;ConsoleOnly;
Keywords=devops;kubernetes;cloud;terraform;deploy;tui;
DESKTOP
    render "$STAGE/$APP.desktop" "$STAGE/$APP.desktop"

    cat > "$STAGE/install.sh" <<'LINSTALL'
#!/bin/sh
# Per-user install of @APP@ (no root required). Override the target
# prefix with PREFIX=/usr/local ./install.sh.
set -eu
here="$(cd "$(dirname "$0")" && pwd -P)"
prefix="${PREFIX:-$HOME/.local}"

install -d "$prefix/bin" "$prefix/share/applications"
install -m 0755 "$here/@APP@" "$prefix/bin/@APP@"
install -m 0644 "$here/@APP@.desktop" "$prefix/share/applications/@APP@.desktop"

for png in "$here"/icons/hicolor/*/apps/@APP@.png; do
	[ -e "$png" ] || continue
	size="$(basename "$(dirname "$(dirname "$png")")")"
	install -d "$prefix/share/icons/hicolor/$size/apps"
	install -m 0644 "$png" "$prefix/share/icons/hicolor/$size/apps/@APP@.png"
done

echo "Installed $prefix/bin/@APP@ (run: @APP@)"
LINSTALL
    render "$STAGE/install.sh" "$STAGE/install.sh"
    chmod 0755 "$STAGE/install.sh"

    cat > "$STAGE/README" <<'LREADME'
@APP@ @VERSION@ (@TARGET@)

  @APP@            the TUI binary (@APP@ @VERSION@ reports the build)
  @APP@.desktop    desktop entry (Terminal=true; the UI is full screen)
  install.sh          per-user install into ~/.local (PREFIX to override)
  icons/hicolor/...   app icons, 16px through 512px

Quick start:  ./install.sh   &&   @APP@
LREADME
    render "$STAGE/README" "$STAGE/README"

    mkdir -p "$STAGE/icons/hicolor"
    for png in "$ICONDIR"/png/*.png; do
        [ -f "$png" ] || continue
        size=$(basename "$png" .png)
        mkdir -p "$STAGE/icons/hicolor/$size/apps"
        cp "$png" "$STAGE/icons/hicolor/$size/apps/$APP.png"
        chmod 0644 "$STAGE/icons/hicolor/$size/apps/$APP.png"
    done

    zip_dir_unix "$STAGE" "$APP-$os-$arch" "$APP" "$APP.desktop" install.sh README icons
    rm -rf "$STAGE"; STAGE=""
}

# make_windows <arch> <bin> — the portable zip: exe, icon, README. The exe
# itself already carries the icon in its PE resources, so no bundle
# wrapper is needed.
make_windows() {
    arch=$1; bin=$2
    RTARGET="windows/$arch"; RARCH=$arch; RGOOS=windows

    STAGE=$(mktemp -d)
    cp "$bin" "$STAGE/$APP.exe"
    chmod 0755 "$STAGE/$APP.exe" 2>/dev/null || true
    cp "$ICONDIR/$APP.ico" "$STAGE/$APP.ico"
    cat > "$STAGE/README.txt" <<'WREADME'
@APP@ @VERSION@ (@TARGET@)

  @APP@.exe    the TUI binary (@APP@.exe version reports the build)
  @APP@.ico    the app icon
  README.txt      this file

Quick start (PowerShell or cmd):

    .\@APP@.exe            open the interactive menu
    .\@APP@.exe install    copy it to %USERPROFILE%\.local\bin\@APP@.exe

The install actions are bash scripts, so bash must be on the PATH (Git
Bash or WSL); Git for Windows is located automatically.
WREADME
    render "$STAGE/README.txt" "$STAGE/README.txt"
    zip_dir_win "$STAGE" "$APP-windows-$arch"
    rm -rf "$STAGE"; STAGE=""
}

for target in $TARGETS; do
    os=${target%%/*}
    arch=${target##*/}
    goarm=""
    [ "$arch" = "arm" ] && goarm=7
    exe=""
    [ "$os" = "windows" ] && exe=".exe"

    # The binary builds into a temp dir: every package below carries it,
    # so no bare binary lands in release/ beside its own package.
    BINDIR=$(mktemp -d)
    bin="$BINDIR/$APP-$os-$arch$exe"
    echo "=== $os/$arch ==="
    build "$os" "$arch" "$goarm" "$bin"

    # Ad-hoc code signature for macOS (only meaningful when run on macOS):
    # kills the "cannot be opened" gatekeeper variant for locally built
    # binaries; unsigned artifacts still quarantine on other machines.
    if [ "$os" = "darwin" ] && command -v codesign >/dev/null 2>&1; then
        codesign --force -s - "$bin" >/dev/null 2>&1 || true
    fi

    case "$os" in
        darwin)         make_darwin "$arch" "$bin" ;;
        linux|freebsd)  make_unix_bundle "$os" "$arch" "$bin" ;;
        windows)        make_windows "$arch" "$bin" ;;
    esac

    rm -rf "$BINDIR"; BINDIR=""
done

echo
echo "== checksums =="
# sha256sum where present (Linux, Git Bash), shasum -a 256 otherwise
# (macOS); sorted for a byte-stable SHA256SUMS across runs.
( cd "$OUT" && for f in "$APP"-*; do
    [ -f "$f" ] || continue
    sha256sum "$f" 2>/dev/null || shasum -a 256 "$f"
done ) | LC_ALL=C sort -k 2 > "$OUT/SHA256SUMS"
cat "$OUT/SHA256SUMS"
echo
echo "done. Release artifacts in $OUT — ship the packaged set; see README.md (unsigned artifacts)."
