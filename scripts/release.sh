#!/usr/bin/env bash
# Cross-compile release artifacts for every supported OS/arch into
# release/ — the builds rolled out to clients and outside users. Dev
# builds of the host binary live in scripts/build.sh.
#
# Thin wrapper around tools/packager, which owns the whole pipeline:
# stripped static cross-compiled binaries, a macOS app bundle and a CLI
# tarball per darwin target, a tarball per linux target, a zip per
# windows target, and release/SHA256SUMS. Keeping the logic there
# (instead of in this script) means this driver and scripts/release.bat
# produce an identical artifact set on any host with the Go toolchain.
#
# The app name is never hardcoded: the packager derives it from the repo
# folder name, so artifacts follow the <APPNAME>-<OS>-<ARCH> convention
# (release/chandler-* in this checkout).
#
# Targets (the darwin/linux list mirrors the supported platforms; windows
# needs Git Bash or WSL at runtime for the bash installers):
#
#   darwin/arm64   <app>-darwin-arm64 + -app.zip + .tar.gz  macOS Apple Silicon
#   darwin/amd64   <app>-darwin-amd64 + -app.zip + .tar.gz  macOS Intel
#   linux/arm      <app>-linux-arm.tar.gz                32-bit ARM (GOARM=7)
#   linux/arm64    <app>-linux-arm64.tar.gz              64-bit ARM servers / SBCs
#   linux/amd64    <app>-linux-amd64.tar.gz              x86-64 servers
#   windows/amd64  <app>-windows-amd64.exe + .zip        Windows x86-64
#   windows/arm64  <app>-windows-arm64.exe + .zip        Windows on ARM
#
# Usage:
#   scripts/release.sh              build every target
#   scripts/release.sh linux        build only linux/* targets
#   scripts/release.sh darwin/arm64 build one target
#
# Every binary is static (CGO_ENABLED=0) and stripped (-s -w, -trimpath),
# so it runs on a fresh machine of its pair without a toolchain. Windows
# binaries carry the app icon from rsrc_windows_*.syso; macOS and Linux
# artifacts carry it from icons/.

set -euo pipefail

cd "$(dirname "$0")/.."
exec go run ./tools/packager "$@"
