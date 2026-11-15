#!/usr/bin/env bash
# Dev build for the host OS — the binary you run while developing.
# Builds rolled out to clients and outside users come from
# release.sh instead.
#
# The app name is derived from the repo folder name, never hardcoded,
# so the script works unchanged in any checkout.
cd "$(dirname "$0")"
# Lowercased like release.sh's artifact names: the dev binary is `chandler`,
# the name .gitignore ignores, whatever the checkout folder is called.
app="$(basename "$PWD" | tr '[:upper:]' '[:lower:]')"
go build -trimpath -ldflags "-s -w" -o "$app" . && echo "built ./$app"
