// Package menu holds the interactive menu's data and action runner: the
// Go port of the MENU_* arrays and of run_menu_action() + run_install()
// from the bash original. Increment 7a ports the data and the runner;
// increment 8b wires the drawing/TUI half (draw.go, colors.go, tui.go)
// and package main's menu phase into them.
package menu

type Item struct {
	Label  string
	Desc   string
	Group  string
	Action string
}

var Items = []Item{
	{Label: "Install Kubectl", Desc: "k8s CLI + kubectx/kubens", Group: "k8s", Action: "kubectl"},
	{Label: "Install K9s", Desc: "k8s terminal UI", Group: "k8s", Action: "k9s"},
	{Label: "Install Helm", Desc: "k8s package manager", Group: "k8s", Action: "helm"},
	{Label: "Install Terraform", Desc: "infrastructure as code", Group: "cloud", Action: "terraform"},
	{Label: "Install AWS CLI", Desc: "aws-cli v2 + SSM plugin", Group: "cloud", Action: "aws"},
	{Label: "Install Brave", Desc: "browser (AppImage)", Group: "app", Action: "brave"},
	{Label: "Install Helium", Desc: "browser (AppImage)", Group: "app", Action: "helium"},
	{Label: "Install Bash tools", Desc: "dotfiles + aliases + k cmd", Group: "app", Action: "bashtools"},
	{Label: "Setup fzf + cursor", Desc: "interactive search (opt-in)", Group: "setup", Action: "fzf-setup"},
	{Label: "Install everything", Desc: "runs 1-9 in order", Group: "bulk", Action: "__all"},
	{Label: "Install core only", Desc: "skips browsers & fzf setup", Group: "bulk", Action: "__core"},
	{Label: "Install oc (OpenShift)", Desc: "OpenShift/kubectl client", Group: "k8s", Action: "oc"},
	{Label: "Config: stacks & cloud", Desc: "choose k8s stack + cloud", Group: "setup", Action: "__config"},
	{Label: "Update installed scripts", Desc: "re-copy custom/ to ~/.local/bin", Group: "setup", Action: "__update_scripts"},
}
