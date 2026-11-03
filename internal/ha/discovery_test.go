package ha

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"twilightzone/ups-mqtt/internal/config"
)

// defaultConfig mirrors what config.Load produces from an empty environment,
// minus the fields discovery does not use.
func defaultConfig() config.Config {
	return config.Config{
		DiscoveryPrefix: "homeassistant",
		StatePrefix:     "twilightzone/ups",
		DeviceID:        "twilightzone_ups",
		DeviceName:      "Twilight Zone UPS",
		DeviceModel:     "EC850LCD",
		PollInterval:    15 * time.Second,
	}
}

// decodePayload unmarshals a discovery payload into a generic map, which is how
// it must be compared: key order is an implementation detail.
func decodePayload(t *testing.T, payload string) map[string]interface{} {
	t.Helper()
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("DiscoveryPayload() is not valid JSON: %v", err)
	}
	return decoded
}

func TestDiscoveryPayload(t *testing.T) {
	got := decodePayload(t, DiscoveryPayload(defaultConfig()))

	want := map[string]interface{}{
		"name":                        "Load",
		"unique_id":                   "twilightzone_ups_load",
		"default_entity_id":           "sensor.twilightzone_ups_load",
		"state_topic":                 "twilightzone/ups/load",
		"availability_topic":          "twilightzone/ups/availability",
		"payload_available":           "online",
		"payload_not_available":       "offline",
		"unit_of_measurement":         "W",
		"device_class":                "power",
		"state_class":                 "measurement",
		"suggested_display_precision": float64(0),
		"expire_after":                float64(60),
		"device": map[string]interface{}{
			"identifiers":  []interface{}{"twilightzone_ups"},
			"name":         "Twilight Zone UPS",
			"manufacturer": "CyberPower",
			"model":        "EC850LCD",
		},
		"origin": map[string]interface{}{
			"name":       "Twilight Zone UPS bridge",
			"sw_version": version,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoveryPayload() = %#v\nwant %#v", got, want)
	}
}

// TestDiscoveryPayloadUsesConfigValues makes sure the payload is derived from
// the configuration instead of repeating the defaults, which is what would
// happen if a value were hardcoded.
func TestDiscoveryPayloadUsesConfigValues(t *testing.T) {
	cfg := config.Config{
		DiscoveryPrefix: "ha",
		StatePrefix:     "site/ups",
		DeviceID:        "lab_ups",
		DeviceName:      "Lab UPS",
		DeviceModel:     "CP585",
		PollInterval:    30 * time.Second,
	}
	got := decodePayload(t, DiscoveryPayload(cfg))

	if got["unique_id"] != "lab_ups_load" {
		t.Errorf("unique_id = %v, want lab_ups_load", got["unique_id"])
	}
	if got["default_entity_id"] != "sensor.lab_ups_load" {
		t.Errorf("default_entity_id = %v, want sensor.lab_ups_load", got["default_entity_id"])
	}
	if got["state_topic"] != "site/ups/load" {
		t.Errorf("state_topic = %v, want site/ups/load", got["state_topic"])
	}
	if got["availability_topic"] != "site/ups/availability" {
		t.Errorf("availability_topic = %v, want site/ups/availability", got["availability_topic"])
	}
	if got["expire_after"] != float64(120) {
		t.Errorf("expire_after = %v, want 120 for a 30s interval", got["expire_after"])
	}

	device, ok := got["device"].(map[string]interface{})
	if !ok {
		t.Fatalf("device = %#v, want an object", got["device"])
	}
	if device["name"] != "Lab UPS" {
		t.Errorf("device.name = %v, want Lab UPS", device["name"])
	}
	if device["model"] != "CP585" {
		t.Errorf("device.model = %v, want CP585", device["model"])
	}
	if !reflect.DeepEqual(device["identifiers"], []interface{}{"lab_ups"}) {
		t.Errorf("device.identifiers = %#v, want [lab_ups]", device["identifiers"])
	}
}

// TestDiscoveryPayloadUniqueIDTracksObjectID guards the coupling between the
// config and ha packages: the slug must stay in sync in the state topic, the
// discovery topic and the entity id.
func TestDiscoveryPayloadUniqueIDTracksObjectID(t *testing.T) {
	cfg := defaultConfig()
	got := decodePayload(t, DiscoveryPayload(cfg))

	wantUniqueID := cfg.DeviceID + "_" + config.LoadObjectID
	if got["unique_id"] != wantUniqueID {
		t.Fatalf("unique_id = %v, want %q", got["unique_id"], wantUniqueID)
	}
	if got["state_topic"] != cfg.StateTopic() {
		t.Fatalf("state_topic = %v, want %q", got["state_topic"], cfg.StateTopic())
	}
	if got["availability_topic"] != cfg.AvailabilityTopic() {
		t.Fatalf("availability_topic = %v, want %q", got["availability_topic"], cfg.AvailabilityTopic())
	}
}

func TestExpireAfter(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		want     int
	}{
		{name: "zero interval falls back to the floor", interval: 0, want: 60},
		{name: "milliseconds fall back to the floor", interval: time.Millisecond, want: 60},
		{name: "one second falls back to the floor", interval: time.Second, want: 60},
		{name: "default interval equals the floor", interval: 15 * time.Second, want: 60},
		{name: "just above the floor", interval: 16 * time.Second, want: 64},
		{name: "one minute scales with the interval", interval: time.Minute, want: 240},
		{name: "ninety seconds scales with the interval", interval: 90 * time.Second, want: 360},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := expireAfter(test.interval); got != test.want {
				t.Fatalf("expireAfter(%s) = %d, want %d", test.interval, got, test.want)
			}
		})
	}
}
