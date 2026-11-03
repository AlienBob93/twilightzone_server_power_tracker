package config

import (
	"strings"
	"testing"
	"time"
)

// testBrokerURL is a valid broker URL used by tests that do not exercise the
// MQTT_URL validation itself.
const testBrokerURL = "tcp://192.168.1.46:1883"

// envKeys lists every variable Load reads. Tests clear all of them first so a
// developer's shell or the surrounding CI job cannot leak configuration in.
var envKeys = []string{
	"MQTT_URL",
	"MQTT_USER",
	"MQTT_PASS",
	"MQTT_CLIENT_ID",
	"MQTT_DISCOVERY_PREFIX",
	"MQTT_STATE_PREFIX",
	"DEVICE_ID",
	"DEVICE_NAME",
	"DEVICE_MODEL",
	"PWRSTAT_PATH",
	"POLL_INTERVAL",
	"PWRSTAT_TIMEOUT",
}

// isolateEnv clears the environment and then applies overrides. Because it
// calls t.Setenv, tests using it must not call t.Parallel.
func isolateEnv(t *testing.T, overrides map[string]string) {
	t.Helper()
	for _, key := range envKeys {
		t.Setenv(key, "")
	}
	for key, value := range overrides {
		t.Setenv(key, value)
	}
}

// withBroker returns overrides that include a valid MQTT_URL.
func withBroker(overrides map[string]string) map[string]string {
	merged := map[string]string{"MQTT_URL": testBrokerURL}
	for key, value := range overrides {
		merged[key] = value
	}
	return merged
}

// mustLoad isolates the environment and fails the test if Load errors.
func mustLoad(t *testing.T, overrides map[string]string) Config {
	t.Helper()
	isolateEnv(t, overrides)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	return cfg
}

// loadErr isolates the environment and fails the test if Load succeeds.
func loadErr(t *testing.T, overrides map[string]string) error {
	t.Helper()
	isolateEnv(t, overrides)
	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load() = %+v, want an error", cfg)
	}
	return err
}

func TestLoadDefaults(t *testing.T) {
	cfg := mustLoad(t, withBroker(nil))

	want := Config{
		BrokerURL:       testBrokerURL,
		ClientID:        "twilightzone-ups",
		DiscoveryPrefix: "homeassistant",
		StatePrefix:     "twilightzone/ups",
		DeviceID:        "twilightzone_ups",
		DeviceName:      "Twilight Zone UPS",
		DeviceModel:     "EC850LCD",
		PwrstatPath:     "/usr/sbin/pwrstat",
		PwrstatTimeout:  10 * time.Second,
		PollInterval:    15 * time.Second,
	}
	if cfg != want {
		t.Fatalf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadEnvironmentOverrides(t *testing.T) {
	cfg := mustLoad(t, withBroker(map[string]string{
		"MQTT_URL":              "mqtt://broker.example:1883",
		"MQTT_USER":             "ups",
		"MQTT_PASS":             "secret",
		"MQTT_CLIENT_ID":        "custom-client",
		"MQTT_DISCOVERY_PREFIX": "/ha/",
		"MQTT_STATE_PREFIX":     "/site/ups/",
		"DEVICE_ID":             "lab-ups",
		"DEVICE_NAME":           "Lab UPS",
		"DEVICE_MODEL":          "CP585",
		"PWRSTAT_PATH":          "/opt/bin/pwrstat",
		"POLL_INTERVAL":         "30",
		"PWRSTAT_TIMEOUT":       "4.5",
	}))

	want := Config{
		BrokerURL:       "mqtt://broker.example:1883",
		Username:        "ups",
		Password:        "secret",
		ClientID:        "custom-client",
		DiscoveryPrefix: "ha",       // surrounding slashes are trimmed
		StatePrefix:     "site/ups", // surrounding slashes are trimmed
		DeviceID:        "lab-ups",
		DeviceName:      "Lab UPS",
		DeviceModel:     "CP585",
		PwrstatPath:     "/opt/bin/pwrstat",
		PwrstatTimeout:  4500 * time.Millisecond,
		PollInterval:    30 * time.Second,
	}
	if cfg != want {
		t.Fatalf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadRequiresBrokerURL(t *testing.T) {
	err := loadErr(t, withBroker(map[string]string{"MQTT_URL": ""}))
	if !strings.Contains(err.Error(), "MQTT_URL is required") {
		t.Fatalf("error = %v, want it to mention MQTT_URL is required", err)
	}
}

func TestLoadRejectsNonBrokerURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "host and port without scheme", url: "192.168.1.46:1883"},
		{name: "scheme without host", url: "tcp://"},
		{name: "empty authority", url: "://192.168.1.46:1883"},
		{name: "path only", url: "/mqtt"},
		{name: "scheme only", url: "tcp:"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := loadErr(t, withBroker(map[string]string{"MQTT_URL": test.url}))
			if !strings.Contains(err.Error(), "MQTT_URL must be a broker URL") {
				t.Fatalf("error = %v, want it to mention the broker URL format", err)
			}
		})
	}
}

func TestLoadDurationValidation(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
		wantParts []string // substrings the error must contain
	}{
		{
			name:      "non numeric poll interval",
			overrides: map[string]string{"POLL_INTERVAL": "soon"},
			wantParts: []string{"POLL_INTERVAL"},
		},
		{
			name:      "zero poll interval",
			overrides: map[string]string{"POLL_INTERVAL": "0"},
			wantParts: []string{"POLL_INTERVAL"},
		},
		{
			name:      "negative poll interval",
			overrides: map[string]string{"POLL_INTERVAL": "-5"},
			wantParts: []string{"POLL_INTERVAL"},
		},
		{
			name:      "non numeric timeout",
			overrides: map[string]string{"PWRSTAT_TIMEOUT": "nope"},
			wantParts: []string{"PWRSTAT_TIMEOUT"},
		},
		{
			name:      "zero timeout",
			overrides: map[string]string{"PWRSTAT_TIMEOUT": "0"},
			wantParts: []string{"PWRSTAT_TIMEOUT"},
		},
		{
			name:      "timeout equal to default interval",
			overrides: map[string]string{"PWRSTAT_TIMEOUT": "15"},
			wantParts: []string{"PWRSTAT_TIMEOUT", "POLL_INTERVAL"},
		},
		{
			name:      "timeout greater than default interval",
			overrides: map[string]string{"PWRSTAT_TIMEOUT": "20"},
			wantParts: []string{"PWRSTAT_TIMEOUT", "POLL_INTERVAL"},
		},
		{
			name:      "timeout not less than a custom interval",
			overrides: map[string]string{"POLL_INTERVAL": "3", "PWRSTAT_TIMEOUT": "3"},
			wantParts: []string{"PWRSTAT_TIMEOUT", "POLL_INTERVAL"},
		},
		{
			name:      "both durations unparsable are reported together",
			overrides: map[string]string{"POLL_INTERVAL": "x", "PWRSTAT_TIMEOUT": "y"},
			wantParts: []string{"POLL_INTERVAL", "PWRSTAT_TIMEOUT"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := loadErr(t, withBroker(test.overrides))
			for _, part := range test.wantParts {
				if !strings.Contains(err.Error(), part) {
					t.Fatalf("error = %v, want it to mention %s", err, part)
				}
			}
		})
	}
}

func TestLoadDurationBoundaryAccepted(t *testing.T) {
	cfg := mustLoad(t, withBroker(map[string]string{
		"POLL_INTERVAL":   "10",
		"PWRSTAT_TIMEOUT": "9.5",
	}))
	if cfg.PollInterval != 10*time.Second {
		t.Fatalf("PollInterval = %s, want 10s", cfg.PollInterval)
	}
	if cfg.PwrstatTimeout != 9500*time.Millisecond {
		t.Fatalf("PwrstatTimeout = %s, want 9.5s", cfg.PwrstatTimeout)
	}
}

func TestLoadCredentialPairing(t *testing.T) {
	t.Run("both set is accepted", func(t *testing.T) {
		cfg := mustLoad(t, withBroker(map[string]string{
			"MQTT_USER": "ups",
			"MQTT_PASS": "secret",
		}))
		if cfg.Username != "ups" || cfg.Password != "secret" {
			t.Fatalf("credentials = (%q, %q), want (%q, %q)", cfg.Username, cfg.Password, "ups", "secret")
		}
	})

	t.Run("neither set is accepted", func(t *testing.T) {
		cfg := mustLoad(t, withBroker(nil))
		if cfg.Username != "" || cfg.Password != "" {
			t.Fatalf("credentials = (%q, %q), want both empty", cfg.Username, cfg.Password)
		}
	})

	t.Run("user without password", func(t *testing.T) {
		err := loadErr(t, withBroker(map[string]string{"MQTT_USER": "ups"}))
		if !strings.Contains(err.Error(), "set both MQTT_USER and MQTT_PASS") {
			t.Fatalf("error = %v, want it to mention the credential pair", err)
		}
	})

	t.Run("password without user", func(t *testing.T) {
		err := loadErr(t, withBroker(map[string]string{"MQTT_PASS": "secret"}))
		if !strings.Contains(err.Error(), "set both MQTT_USER and MQTT_PASS") {
			t.Fatalf("error = %v, want it to mention the credential pair", err)
		}
	})
}

func TestLoadDeviceIDValidation(t *testing.T) {
	valid := []string{"twilightzone_ups", "UPS-1", "abc123", "a_b-c"}
	for _, id := range valid {
		t.Run("valid "+id, func(t *testing.T) {
			cfg := mustLoad(t, withBroker(map[string]string{"DEVICE_ID": id}))
			if cfg.DeviceID != id {
				t.Fatalf("DeviceID = %q, want %q", cfg.DeviceID, id)
			}
		})
	}

	invalid := []string{"twilight zone", "twilightzone/ups", "ups!", "twilightzone+ups", "üps"}
	for _, id := range invalid {
		t.Run("invalid "+id, func(t *testing.T) {
			err := loadErr(t, withBroker(map[string]string{"DEVICE_ID": id}))
			if !strings.Contains(err.Error(), "DEVICE_ID may contain only") {
				t.Fatalf("error = %v, want it to mention the DEVICE_ID character set", err)
			}
		})
	}
}

func TestTopicsWithDefaults(t *testing.T) {
	cfg := mustLoad(t, withBroker(nil))

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "state", got: cfg.StateTopic(), want: "twilightzone/ups/load"},
		{name: "availability", got: cfg.AvailabilityTopic(), want: "twilightzone/ups/availability"},
		{name: "discovery", got: cfg.DiscoveryTopic(), want: "homeassistant/sensor/twilightzone_ups/load/config"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("%s topic = %q, want %q", test.name, test.got, test.want)
			}
		})
	}
}

func TestTopicsWithCustomPrefixes(t *testing.T) {
	cfg := mustLoad(t, withBroker(map[string]string{
		"MQTT_STATE_PREFIX":     "/site/ups/",
		"MQTT_DISCOVERY_PREFIX": "ha",
		"DEVICE_ID":             "lab-ups",
	}))

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "state", got: cfg.StateTopic(), want: "site/ups/load"},
		{name: "availability", got: cfg.AvailabilityTopic(), want: "site/ups/availability"},
		{name: "discovery", got: cfg.DiscoveryTopic(), want: "ha/sensor/lab-ups/load/config"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("%s topic = %q, want %q", test.name, test.got, test.want)
			}
		})
	}
}

func TestTopicSegmentSafe(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "lowercase", value: "twilightzone", want: true},
		{name: "mixed case with digits", value: "UPS-1a", want: true},
		{name: "underscore and hyphen", value: "a_b-c", want: true},
		{name: "empty", value: "", want: false},
		{name: "space", value: "twilight zone", want: false},
		{name: "slash", value: "twilight/ups", want: false},
		{name: "plus", value: "ups+1", want: false},
		{name: "dot", value: "twilight.ups", want: false},
		{name: "non ascii", value: "üps", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := topicSegmentSafe(test.value); got != test.want {
				t.Fatalf("topicSegmentSafe(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestLoadObjectID(t *testing.T) {
	// The constant is shared with the ha package, where it forms the entity's
	// unique_id, so pin it to the documented slug.
	if LoadObjectID != "load" {
		t.Fatalf("LoadObjectID = %q, want %q", LoadObjectID, "load")
	}
}
