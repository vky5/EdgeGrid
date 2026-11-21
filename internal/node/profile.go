package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type appConfig struct {
	Active string `json:"active"`
}

// the folder that holds every profile, ~/.edgegrid, made if missing
func ProfileRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	root := filepath.Join(home, ".edgegrid")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("create %s: %w", root, err)
	}
	return root, nil
}

func appJSONPath(root string) string { return filepath.Join(root, "app.json") }

// the active profile's name, e.g. "primary", or "" if none is set
func ActiveProfile() string {
	root, err := ProfileRoot()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(appJSONPath(root))
	if err != nil {
		return ""
	}
	var cfg appConfig
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	return cfg.Active
}

// the active profile's data folder, e.g. ~/.edgegrid/primary, or "" if none is active
// callers then use their own default, ./data
func ProfileDir() string {
	name := ActiveProfile()
	if name == "" {
		return ""
	}
	root, err := ProfileRoot()
	if err != nil {
		return ""
	}
	return filepath.Join(root, name)
}

// makes a profile the active one, creating its folder if it's new
func UseProfile(name string) error {
	if name == "" {
		return fmt.Errorf("profile name required")
	}
	root, err := ProfileRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}
	data, err := json.MarshalIndent(appConfig{Active: name}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(appJSONPath(root), data, 0600)
}

// every profile name, one per folder in ~/.edgegrid, sorted
func ListProfiles() ([]string, error) {
	root, err := ProfileRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// removes a profile's folder, and clears the active profile if it was that one
func DeleteProfile(name string) error {
	if name == "" {
		return fmt.Errorf("profile name required")
	}
	root, err := ProfileRoot()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete profile dir: %w", err)
	}
	if ActiveProfile() == name {
		data, err := json.MarshalIndent(appConfig{Active: ""}, "", "  ")
		if err == nil {
			_ = os.WriteFile(appJSONPath(root), data, 0600)
		}
	}
	return nil
}
