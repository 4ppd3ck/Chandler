@echo off
rem Cross-compile release artifacts for every supported OS/arch into
rem release\ — the builds rolled out to clients and outside users. Dev
rem builds of the host binary live in scripts\build.bat.
rem
rem Thin wrapper with the same contract as scripts\release.sh: both
rem delegate to `go run ./tools/packager`, which owns the whole pipeline
rem (stripped static cross-compiled binaries, macOS app bundles + CLI
rem tarballs, Linux tarballs, Windows zips, release\SHA256SUMS). Either
rem driver therefore yields an identical artifact set.
rem
rem The app name is never hardcoded: the packager derives it from the repo
rem folder name, so artifacts follow the <APPNAME>-<OS>-<ARCH> convention
rem (release\chandler-* in this checkout).
rem
rem Usage: scripts\release.bat [targets]   e.g. scripts\release.bat linux
cd /d "%~dp0.."
go run ./tools/packager %*
exit /b %errorlevel%
