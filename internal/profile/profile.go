// Package profile manages named data-dir profiles under ~/.edgegrid, so
// nodes can be identified by a short name ("primary", "worker-test")
// instead of an arbitrary --data-dir path. Exactly one profile is "active"
// at a time (tracked in ~/.edgegrid/app.json) and is used as the default
// data dir when no --data-dir flag or DATA_DIR env var is given.
package profile

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

// Root returns ~/.edgegrid, creating it if it doesn't exist yet.
func Root() (string, error) {
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

// Active returns the currently active profile name, or "" if none has been
// set (e.g. fresh install, or the profile system is simply unused).
func Active() string {
	root, err := Root()
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

// Dir returns the data directory for the active profile, or "" if no
// profile is active — callers fall back to their own default (./data) in
// that case, same as if the profile system didn't exist.
func Dir() string {
	name := Active()
	if name == "" {
		return ""
	}
	root, err := Root()
	if err != nil {
		return ""
	}
	return filepath.Join(root, name)
}

// Use makes name the active profile, creating its data dir if it's new.
func Use(name string) error {
	if name == "" {
		return fmt.Errorf("profile name required")
	}
	root, err := Root()
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

// List returns all known profile names (subdirectories of the profile
// root), sorted alphabetically.
func List() ([]string, error) {
	root, err := Root()
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

// Delete removes the profile's data directory and resets active if it was deleted.
func Delete(name string) error {
	if name == "" {
		return fmt.Errorf("profile name required")
	}
	root, err := Root()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete profile dir: %w", err)
	}
	if Active() == name {
		data, err := json.MarshalIndent(appConfig{Active: ""}, "", "  ")
		if err == nil {
			_ = os.WriteFile(appJSONPath(root), data, 0600)
		}
	}
	return nil
}
