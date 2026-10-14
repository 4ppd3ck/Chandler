@echo off
rem Dev build for Windows — the binary you run while developing.
rem Builds rolled out to clients and outside users come from
rem scripts\release.bat instead.
rem The app name is derived from the repo folder name, never hardcoded,
rem so the script works unchanged in any checkout.
cd /d "%~dp0.."
for %%I in (".") do set "APP=%%~nxI"
go build -trimpath -ldflags "-s -w" -o "%APP%.exe" . && echo built %APP%.exe
