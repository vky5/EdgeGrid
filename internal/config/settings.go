package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const settingsFile = "settings.json"

// ProfileSettings is the durable per-profile config stored under the
// profile data dir (e.g. ~/.edgegrid/worker-n/settings.json).
// Tokens (admin.token, node.token, …) stay separate files; this holds
// ports, executor, and other knobs users change from "Configure Settings".
//
// Zero / empty fields mean "leave Config defaults alone" on Apply.
type ProfileSettings struct {
	// Role is a hint for the settings UI: "primary", "secondary", "worker", or "".
	Role string `json:"role,omitempty"`

	NATSPort    int    `json:"nats_port,omitempty"`
	ClusterPort int    `json:"cluster_port,omitempty"`
	ClusterName string `json:"cluster_name,omitempty"`
	// APIPort is the coordinator HTTP listen port, with or without leading ":".
	APIPort string `json:"api_port,omitempty"`

	Executor        string `json:"executor,omitempty"` // "training" | "mock"
	RequireApproval *bool  `json:"require_approval,omitempty"`

	JoinURL           string `json:"join_url,omitempty"`
	TailscaleHostname string `json:"ts_hostname,omitempty"`
}

func settingsPath(dataDir string) string {
	return filepath.Join(dataDir, settingsFile)
}

// LoadProfileSettings reads dataDir/settings.json. Missing file → empty settings, nil error.
func LoadProfileSettings(dataDir string) (ProfileSettings, error) {
	if dataDir == "" {
		return ProfileSettings{}, nil
	}
	b, err := os.ReadFile(settingsPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return ProfileSettings{}, nil
		}
		return ProfileSettings{}, err
	}
	var s ProfileSettings
	if err := json.Unmarshal(b, &s); err != nil {
		return ProfileSettings{}, fmt.Errorf("parse %s: %w", settingsPath(dataDir), err)
	}
	return s, nil
}

// SaveProfileSettings writes settings with 0600 permissions.
func SaveProfileSettings(dataDir string, s ProfileSettings) error {
	if dataDir == "" {
		return fmt.Errorf("data dir required")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	if err := s.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(dataDir), b, 0600)
}

// Validate checks ports and executor when set.
func (s ProfileSettings) Validate() error {
	if s.NATSPort != 0 && (s.NATSPort < 1 || s.NATSPort > 65535) {
		return fmt.Errorf("nats_port must be 1–65535")
	}
	if s.ClusterPort != 0 && (s.ClusterPort < 1 || s.ClusterPort > 65535) {
		return fmt.Errorf("cluster_port must be 1–65535")
	}
	if s.APIPort != "" {
		p := strings.TrimPrefix(s.APIPort, ":")
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("api_port must be 1–65535")
		}
	}
	if s.Executor != "" {
		switch strings.ToLower(s.Executor) {
		case "training", "mock":
		default:
			return fmt.Errorf("executor must be \"training\" or \"mock\"")
		}
	}
	return nil
}

// Apply merges non-zero settings into cfg. Call after LoadConfig and after
// setting cfg.DataDir so agent startup picks up profile knobs.
func (s ProfileSettings) Apply(cfg *Config) {
	if cfg == nil {
		return
	}
	if s.NATSPort > 0 {
		cfg.NATSPort = s.NATSPort
	}
	if s.ClusterPort > 0 {
		cfg.ClusterPort = s.ClusterPort
	}
	if s.ClusterName != "" {
		cfg.ClusterName = s.ClusterName
	}
	if s.APIPort != "" {
		p := s.APIPort
		if !strings.HasPrefix(p, ":") {
			p = ":" + p
		}
		cfg.Server.Port = p
	}
	if s.Executor != "" {
		cfg.Client.Executor = strings.ToLower(s.Executor)
	}
	if s.RequireApproval != nil {
		cfg.Client.RequireApproval = *s.RequireApproval
	}
	if s.JoinURL != "" {
		cfg.JoinURL = s.JoinURL
	}
	if s.TailscaleHostname != "" {
		cfg.TailscaleHostname = s.TailscaleHostname
	}
}

// ApplyProfileSettings loads dataDir/settings.json and applies it to cfg.
func ApplyProfileSettings(cfg *Config) {
	if cfg == nil || cfg.DataDir == "" {
		return
	}
	s, err := LoadProfileSettings(cfg.DataDir)
	if err != nil {
		return
	}
	s.Apply(cfg)
}

// SnapshotFromConfig builds ProfileSettings from the live Config (for first save
// from onboarding). Role should be set by the caller when known.
func SnapshotFromConfig(cfg *Config, role string) ProfileSettings {
	if cfg == nil {
		return ProfileSettings{Role: role}
	}
	req := cfg.Client.RequireApproval
	return ProfileSettings{
		Role:              role,
		NATSPort:          cfg.NATSPort,
		ClusterPort:       cfg.ClusterPort,
		ClusterName:       cfg.ClusterName,
		APIPort:           cfg.Server.Port,
		Executor:          cfg.Client.Executor,
		RequireApproval:   &req,
		JoinURL:           cfg.JoinURL,
		TailscaleHostname: cfg.TailscaleHostname,
	}
}

// DetectRoleHint reports this profile's role: "primary", "secondary", or
// "worker" (empty if unknown).
//
// Prefers the role persisted to settings.json at onboarding's role-selection
// step (see onboarding/role.go) — written the moment a role is confirmed,
// before any join/approval even starts, so it's unaffected by whether that
// flow later completes. Only falls back to inferring from which credential
// files exist for profiles that never went through the wizard (headless
// --server/--client launches) or predate this being tracked.
//
// The fallback matters: a secondary coordinator generates admin.token only
// after a successful join+approval (see agent/build.go's buildCoordinator).
// A node whose first run was interrupted between saving node.token and
// reaching that point — or any run that read the *inferred* role instead of
// the persisted one and started as a plain worker as a result — never gets
// admin.token, permanently misclassifying it as a worker on every future
// restart too (a worker-configured run never calls buildCoordinator either).
// Persisting role directly at selection time breaks that loop.
func DetectRoleHint(dataDir string) string {
	if s, err := LoadProfileSettings(dataDir); err == nil && s.Role != "" {
		return s.Role
	}
	admin := fileExists(filepath.Join(dataDir, "admin.token"))
	node := fileExists(filepath.Join(dataDir, "node.token"))
	switch {
	case admin && node:
		return "secondary" // has coordinator creds and was itself approved into another cluster
	case admin:
		return "primary"
	case node:
		return "worker"
	default:
		return ""
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
