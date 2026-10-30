// Package config loads UPS Monitor configuration from an optional YAML file
// and environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	NUT       NUTConfig       `yaml:"nut"`
	Collector CollectorConfig `yaml:"collector"`
	Storage   StorageConfig   `yaml:"storage"`
	Logging   LoggingConfig   `yaml:"logging"`
}

type ServerConfig struct {
	Address        string `yaml:"address"`
	RefreshSeconds int    `yaml:"refresh_seconds"`
}

type NUTConfig struct {
	Host     string   `yaml:"host"`
	Port     int      `yaml:"port"`
	UPS      string   `yaml:"ups"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
	Timeout  Duration `yaml:"timeout"`
}

type CollectorConfig struct {
	Interval Duration `yaml:"interval"`
}

type StorageConfig struct {
	Path      string   `yaml:"path"`
	Retention Duration `yaml:"retention"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Duration is a time.Duration that accepts either Go duration strings ("30s",
// "12h") or a number of seconds in YAML.
type Duration time.Duration

// Std returns the value as a standard time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag == "!!int" || value.Tag == "!!float" {
		var seconds float64
		if err := value.Decode(&seconds); err != nil {
			return fmt.Errorf("invalid duration: %w", err)
		}
		*d = Duration(time.Duration(seconds * float64(time.Second)))
		return nil
	}

	var raw string
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("duration must be a string like \"30s\" or a number of seconds: %w", err)
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	*d = Duration(parsed)
	return nil
}

// Default returns the configuration used when no file and no environment
// overrides are present.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Address:        "0.0.0.0:8080",
			RefreshSeconds: 10,
		},
		NUT: NUTConfig{
			Host:    "localhost",
			Port:    3493,
			Timeout: Duration(5 * time.Second),
		},
		Collector: CollectorConfig{
			Interval: Duration(10 * time.Second),
		},
		Storage: StorageConfig{
			Path:      "./data/ups-monitor.db",
			Retention: Duration(720 * time.Hour),
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

// Load reads configuration from path (when non-empty), applies environment
// overrides, validates the result and returns it.
func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	if err := applyEnv(&cfg); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func applyEnv(cfg *Config) error {
	envString("HTTP_ADDR", &cfg.Server.Address)
	envString("DB_PATH", &cfg.Storage.Path)
	envString("NUT_HOST", &cfg.NUT.Host)
	envString("NUT_UPS", &cfg.NUT.UPS)
	envString("NUT_USERNAME", &cfg.NUT.Username)
	envString("NUT_PASSWORD", &cfg.NUT.Password)
	envString("LOG_LEVEL", &cfg.Logging.Level)
	envString("LOG_FORMAT", &cfg.Logging.Format)

	if err := envInt("NUT_PORT", &cfg.NUT.Port); err != nil {
		return err
	}
	if err := envInt("REFRESH_SECONDS", &cfg.Server.RefreshSeconds); err != nil {
		return err
	}
	if err := envDuration("NUT_TIMEOUT", &cfg.NUT.Timeout); err != nil {
		return err
	}
	if err := envDuration("POLL_INTERVAL", &cfg.Collector.Interval); err != nil {
		return err
	}
	if err := envDuration("RETENTION", &cfg.Storage.Retention); err != nil {
		return err
	}
	return nil
}

// Validate checks the values that the rest of the application relies on.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Server.Address) == "" {
		return errors.New("server.address must not be empty")
	}
	if c.Server.RefreshSeconds < 1 {
		return errors.New("server.refresh_seconds must be at least 1")
	}
	if strings.TrimSpace(c.NUT.Host) == "" {
		return errors.New("nut.host must not be empty")
	}
	if c.NUT.Port < 1 || c.NUT.Port > 65535 {
		return errors.New("nut.port must be between 1 and 65535")
	}
	if c.NUT.Timeout.Std() <= 0 {
		return errors.New("nut.timeout must be greater than zero")
	}
	if c.Collector.Interval.Std() < time.Second {
		return errors.New("collector.interval must be at least 1s")
	}
	if strings.TrimSpace(c.Storage.Path) == "" {
		return errors.New("storage.path must not be empty")
	}
	if c.Storage.Retention.Std() < 0 {
		return errors.New("storage.retention must be zero or greater")
	}

	switch strings.ToLower(c.Logging.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level must be one of debug, info, warn or error, got %q", c.Logging.Level)
	}
	switch strings.ToLower(c.Logging.Format) {
	case "text", "json":
	default:
		return fmt.Errorf("logging.format must be text or json, got %q", c.Logging.Format)
	}
	return nil
}

// SlogLevel maps logging.level to a slog level.
func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.Logging.Level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func envString(key string, target *string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		*target = value
	}
}

func envInt(key string, target *int) error {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%s must be an integer: %w", key, err)
	}
	*target = parsed
	return nil
}

func envDuration(key string, target *Duration) error {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s must be a duration such as 10s or 720h: %w", key, err)
	}
	*target = Duration(parsed)
	return nil
}
