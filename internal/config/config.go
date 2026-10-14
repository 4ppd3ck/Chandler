// Package config loads the deployment root's optional config.env into
// the process environment, the way the bash original sources
// STACK_CONFIG_FILE near the top of the script, and saves it back (Save)
// the way ask_stack_cloud() writes it, so later child installers inherit
// CHANDLER_K8S_STACKS / CHANDLER_CLOUD_PROVIDERS.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	KeyK8sStacks      = "CHANDLER_K8S_STACKS"
	KeyCloudProviders = "CHANDLER_CLOUD_PROVIDERS"
)

var keys = []string{KeyK8sStacks, KeyCloudProviders}

func Load(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "config.env"))
	if err != nil {
		unsetAll()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	values := parse(string(data))
	for _, key := range keys {
		if value, ok := values[key]; ok {
			os.Setenv(key, value)
		} else {
			os.Unsetenv(key)
		}
	}
	return nil
}

const headerLine = "# Written by chandler — edit or re-run the menu's 'Config' entry."

func Save(root, stacks, clouds string) error {
	contents := fmt.Sprintf("%s\nexport %s=\"%s\"\nexport %s=\"%s\"\n",
		headerLine, KeyK8sStacks, stacks, KeyCloudProviders, clouds)
	if err := os.WriteFile(filepath.Join(root, "config.env"), []byte(contents), 0o644); err != nil {
		return err
	}
	os.Setenv(KeyK8sStacks, stacks)
	os.Setenv(KeyCloudProviders, clouds)
	return nil
}

func parse(contents string) map[string]string {
	values := make(map[string]string)
	for _, raw := range strings.Split(contents, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if rest, ok := strings.CutPrefix(key, "export"); ok {
			key = strings.TrimSpace(rest)
		}
		if key == KeyK8sStacks || key == KeyCloudProviders {
			values[key] = unquote(strings.TrimSpace(value))
		}
	}
	return values
}

func unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func unsetAll() {
	for _, key := range keys {
		os.Unsetenv(key)
	}
}
