package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var envKeys = []string{
	"HTTP_ADDR", "DB_PATH",
	"NUT_HOST", "NUT_PORT", "NUT_UPS", "NUT_USERNAME", "NUT_PASSWORD",
	"NUT_TIMEOUT",
	"LOG_LEVEL", "LOG_FORMAT", "REFRESH_SECONDS",
	"POLL_INTERVAL", "RETENTION",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range envKeys {
		t.Setenv(key, "")
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Address != "0.0.0.0:8080" {
		t.Errorf("address = %q", cfg.Server.Address)
	}
	if cfg.NUT.Host != "localhost" || cfg.NUT.Port != 3493 {
		t.Errorf("nut = %s:%d", cfg.NUT.Host, cfg.NUT.Port)
	}
	if cfg.Collector.Interval.Std() != 10*time.Second {
		t.Errorf("interval = %s", cfg.Collector.Interval)
	}
	if cfg.Storage.Path != "./data/ups-monitor.db" {
		t.Errorf("storage path = %q", cfg.Storage.Path)
	}
	if cfg.Storage.Retention.Std() != 720*time.Hour {
		t.Errorf("retention = %s", cfg.Storage.Retention)
	}
	if cfg.SlogLevel().String() != "INFO" {
		t.Errorf("log level = %s", cfg.SlogLevel())
	}
}

func TestLoadYAML(t *testing.T) {
	clearEnv(t)

	path := writeConfig(t, `
server:
  address: "127.0.0.1:9090"
nut:
  host: "nut.internal"
  port: 1234
  timeout: 2s
collector:
  interval: 3s
storage:
  path: "/tmp/ups.db"
  retention: 24h
logging:
  level: "debug"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Address != "127.0.0.1:9090" {
		t.Errorf("address = %q", cfg.Server.Address)
	}
	if cfg.Server.RefreshSeconds != 10 {
		t.Errorf("refresh_seconds default was not preserved: %d", cfg.Server.RefreshSeconds)
	}
	if cfg.NUT.Host != "nut.internal" || cfg.NUT.Port != 1234 {
		t.Errorf("nut = %s:%d", cfg.NUT.Host, cfg.NUT.Port)
	}
	if cfg.NUT.Timeout.Std() != 2*time.Second {
		t.Errorf("timeout = %s", cfg.NUT.Timeout)
	}
	if cfg.Collector.Interval.Std() != 3*time.Second {
		t.Errorf("interval = %s", cfg.Collector.Interval)
	}
	if cfg.Storage.Retention.Std() != 24*time.Hour {
		t.Errorf("retention = %s", cfg.Storage.Retention)
	}
	if cfg.Logging.Format != "text" {
		t.Errorf("format default was not preserved: %q", cfg.Logging.Format)
	}
	if cfg.SlogLevel().String() != "DEBUG" {
		t.Errorf("log level = %s", cfg.SlogLevel())
	}
}

func TestNumericDurations(t *testing.T) {
	clearEnv(t)

	path := writeConfig(t, `
collector:
  interval: 4
storage:
  retention: 3600
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Collector.Interval.Std() != 4*time.Second {
		t.Errorf("interval = %s", cfg.Collector.Interval)
	}
	if cfg.Storage.Retention.Std() != time.Hour {
		t.Errorf("retention = %s", cfg.Storage.Retention)
	}
}

func TestEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("NUT_HOST", "override.local")
	t.Setenv("NUT_PORT", "9999")
	t.Setenv("NUT_UPS", "rack")
	t.Setenv("POLL_INTERVAL", "5s")
	t.Setenv("RETENTION", "0")
	t.Setenv("DB_PATH", "/tmp/override.db")
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("LOG_LEVEL", "warn")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NUT.Host != "override.local" || cfg.NUT.Port != 9999 || cfg.NUT.UPS != "rack" {
		t.Errorf("nut = %+v", cfg.NUT)
	}
	if cfg.Collector.Interval.Std() != 5*time.Second {
		t.Errorf("interval = %s", cfg.Collector.Interval)
	}
	if cfg.Storage.Retention.Std() != 0 {
		t.Errorf("retention = %s", cfg.Storage.Retention)
	}
	if cfg.Storage.Path != "/tmp/override.db" || cfg.Server.Address != ":9090" {
		t.Errorf("storage/server = %q / %q", cfg.Storage.Path, cfg.Server.Address)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		yaml    string
		wantErr string
	}{
		{
			name:    "bad port",
			env:     map[string]string{"NUT_PORT": "nope"},
			wantErr: "NUT_PORT",
		},
		{
			name:    "bad interval",
			env:     map[string]string{"POLL_INTERVAL": "soon"},
			wantErr: "POLL_INTERVAL",
		},
		{
			name:    "port out of range",
			env:     map[string]string{"NUT_PORT": "70000"},
			wantErr: "nut.port",
		},
		{
			name:    "bad log level",
			yaml:    "logging:\n  level: loud\n",
			wantErr: "logging.level",
		},
		{
			name:    "bad yaml",
			yaml:    "server: [",
			wantErr: "parse config",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearEnv(t)
			for key, value := range test.env {
				t.Setenv(key, value)
			}

			path := ""
			if test.yaml != "" {
				path = writeConfig(t, test.yaml)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error %q does not contain %q", err, test.wantErr)
			}
		})
	}
}

func TestMissingFile(t *testing.T) {
	clearEnv(t)

	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected an error for an explicit missing config file")
	}
}
