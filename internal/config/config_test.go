package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useTempDir points Load and Path at a fresh directory and returns the
// config.yaml path they will use.
func useTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	path := filepath.Join(dir, "StayWakeBlackScreen", fileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCreatesDefaultFile(t *testing.T) {
	path := useTempDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg != defaults() {
		t.Errorf("first Load = %+v, want defaults %+v", cfg, defaults())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("default config.yaml was not created: %v", err)
	}
	for _, line := range []string{"idle_minutes: 3", "heartbeat_seconds: 5", "poll_ms: 250", "start_enabled: true"} {
		if !strings.Contains(string(data), line) {
			t.Errorf("created config.yaml is missing %q", line)
		}
	}

	again, err := Load()
	if err != nil || again != defaults() {
		t.Errorf("reloading the generated file = %+v, %v; want defaults, nil", again, err)
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want Config
	}{
		{
			name: "file predating newer fields keeps their defaults",
			yaml: "idle_minutes: 7\n",
			want: Config{IdleMinutes: 7, HeartbeatSeconds: 5, PollMs: 250, StartEnabled: true},
		},
		{
			name: "every field overridden",
			yaml: "idle_minutes: 10\nheartbeat_seconds: 2\npoll_ms: 500\nstart_enabled: false\n",
			want: Config{IdleMinutes: 10, HeartbeatSeconds: 2, PollMs: 500, StartEnabled: false},
		},
		{
			name: "invalid values fall back per field",
			yaml: "idle_minutes: -1\nheartbeat_seconds: 0\npoll_ms: -5\nstart_enabled: false\n",
			want: Config{IdleMinutes: 3, HeartbeatSeconds: 5, PollMs: 250, StartEnabled: false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := useTempDir(t)
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg != tt.want {
				t.Errorf("Load = %+v, want %+v", cfg, tt.want)
			}
		})
	}
}

func TestLoadMalformedYAMLFallsBackToDefaults(t *testing.T) {
	path := useTempDir(t)
	if err := os.WriteFile(path, []byte("idle_minutes: [not a number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err == nil {
		t.Error("Load returned no error for malformed YAML")
	}
	if cfg != defaults() {
		t.Errorf("Load = %+v, want defaults %+v", cfg, defaults())
	}
}

func TestPath(t *testing.T) {
	want := useTempDir(t)
	got, err := Path()
	if err != nil || got != want {
		t.Errorf("Path() = %q, %v; want %q, nil", got, err, want)
	}
}
