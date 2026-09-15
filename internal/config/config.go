// Package config reads the user-editable config.yaml that lives next to
// the installed app's data, in the same per-user directory the installer
// uses (%LOCALAPPDATA%\StayWakeBlackScreen). It is created with default
// values the first time it's loaded, so the user always has a real file
// to edit rather than having to know the option names up front.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Defaults written to a freshly created config.yaml, and also the
// fallback used if the file is missing, unreadable, or has an invalid
// value for the corresponding field.
const (
	DefaultIdleMinutes      = 3
	DefaultHeartbeatSeconds = 5
	DefaultPollMs           = 250
	DefaultStartEnabled     = true
)

const fileName = "config.yaml"

const template = `# StayWakeBlackScreen configuration
#
# idle_minutes: minutes of inactivity (no keyboard/mouse input) before the
# screen blacks out. Can still be overridden per-run with -idle-minutes.
idle_minutes: %d

# heartbeat_seconds: how often (in seconds), while blacked out, the
# program toggles Caps Lock as a harmless "still alive" signal that keeps
# Windows from treating the session as idle. Can still be overridden
# per-run with -heartbeat-seconds.
heartbeat_seconds: %d

# poll_ms: how often (in milliseconds) the program checks for idle time
# and for the Escape key while blacked out. Lower is more responsive but
# uses slightly more CPU. Can still be overridden per-run with -poll-ms.
poll_ms: %d

# start_enabled: whether the idle guard is active as soon as the program
# starts (true), or starts paused - no blackout, no sleep blocking - until
# enabled from the tray menu (false).
start_enabled: %t
`

// Config holds the settings read from config.yaml.
type Config struct {
	IdleMinutes      int  `yaml:"idle_minutes"`
	HeartbeatSeconds int  `yaml:"heartbeat_seconds"`
	PollMs           int  `yaml:"poll_ms"`
	StartEnabled     bool `yaml:"start_enabled"`
}

func defaults() Config {
	return Config{
		IdleMinutes:      DefaultIdleMinutes,
		HeartbeatSeconds: DefaultHeartbeatSeconds,
		PollMs:           DefaultPollMs,
		StartEnabled:     DefaultStartEnabled,
	}
}

// Load reads config.yaml, creating it with default values on first run.
// Any error, or an invalid value for a given field, falls back to that
// field's default rather than failing - a bad or missing config file
// should never stop the program from starting. A config.yaml written
// before a field existed (e.g. an old file with only idle_minutes) is
// treated the same as that field being absent: the field keeps its
// default rather than being reset to zero.
func Load() (Config, error) {
	def := defaults()

	dir, err := userDir()
	if err != nil {
		return def, err
	}
	path := filepath.Join(dir, fileName)

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		text := fmt.Sprintf(template, DefaultIdleMinutes, DefaultHeartbeatSeconds, DefaultPollMs, DefaultStartEnabled)
		if werr := os.WriteFile(path, []byte(text), 0o644); werr != nil {
			return def, fmt.Errorf("writing default config.yaml: %w", werr)
		}
		return def, nil
	}
	if err != nil {
		return def, fmt.Errorf("reading config.yaml: %w", err)
	}

	cfg := def
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return def, fmt.Errorf("parsing config.yaml: %w", err)
	}
	if cfg.IdleMinutes < 1 {
		cfg.IdleMinutes = DefaultIdleMinutes
	}
	if cfg.HeartbeatSeconds < 1 {
		cfg.HeartbeatSeconds = DefaultHeartbeatSeconds
	}
	if cfg.PollMs < 1 {
		cfg.PollMs = DefaultPollMs
	}
	return cfg, nil
}

func userDir() (string, error) {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolving user config directory: %w", err)
		}
	}
	dir = filepath.Join(dir, "StayWakeBlackScreen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating config directory: %w", err)
	}
	return dir, nil
}
