#!/usr/bin/env bash
# Dev build for the host OS — the binary you run while developing.
# Builds rolled out to clients and outside users come from
# scripts/release.sh instead.
#
# The app name is derived from the repo folder name, never hardcoded,
# so the script works unchanged in any checkout.
cd "$(dirname "$0")/.."
app="$(basename "$PWD")"
go build -trimpath -ldflags "-s -w" -o "$app" . && echo "built ./$app"
