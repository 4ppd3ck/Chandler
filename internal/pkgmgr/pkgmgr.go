// Package pkgmgr ports the package-manager helpers of
// the bash original: detect_pkg_manager() and install_pkg().
package pkgmgr

import (
	"fmt"
	"os"
	"os/exec"
)

type probe struct {
	bin     string
	manager string
}

var probesPerOS = map[string][]probe{
	"macos": {
		{bin: "brew", manager: "brew"},
		{bin: "port", manager: "port"},
	},
	"linux": {
		{bin: "apt-get", manager: "apt"},
		{bin: "dnf", manager: "dnf"},
		{bin: "yum", manager: "yum"},
		{bin: "pacman", manager: "pacman"},
		{bin: "zypper", manager: "zypper"},
		{bin: "brew", manager: "brew"},
	},
	"windows": {
		{bin: "choco", manager: "choco"},
		{bin: "scoop", manager: "scoop"},
		{bin: "winget", manager: "winget"},
	},
}

func DetectPkgManager(goos string) string {
	for _, p := range probesPerOS[goos] {
		if _, err := exec.LookPath(p.bin); err == nil {
			return p.manager
		}
	}
	return ""
}

func InstallPkg(manager, brewPkg, aptPkg, dnfPkg, chocoPkg string) error {
	switch manager {
	case "brew":
		return run("brew", "install", brewPkg)
	case "port": // MacPorts uses same name usually
		return run("port", "install", brewPkg)
	case "apt":
		if err := run("apt-get", "update", "-qq"); err != nil {
			return err
		}
		return run("apt-get", "install", "-y", aptPkg)
	case "dnf":
		return run("dnf", "install", "-y", dnfPkg)
	case "yum":
		return run("yum", "install", "-y", dnfPkg)
	case "pacman":
		return run("pacman", "-Sy", "--noconfirm", dnfPkg)
	case "zypper":
		return run("zypper", "--non-interactive", "install", dnfPkg)
	case "choco":
		return run("choco", "install", chocoPkg, "-y")
	case "scoop":
		return run("scoop", "install", chocoPkg)
	case "winget":
		return run("winget", "install", "--accept-source-agreements", "--accept-package-agreements", chocoPkg)
	default:
		return fmt.Errorf("unknown package manager: %s", manager)
	}
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
