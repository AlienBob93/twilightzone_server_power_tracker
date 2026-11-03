// Package ha builds the Home Assistant MQTT discovery payload for the bridge's
// single sensor.
package ha

import (
	"encoding/json"
	"fmt"
	"time"

	"twilightzone/ups-mqtt/internal/config"
)

const (
	// version is reported to Home Assistant as the origin software version.
	version = "0.2.0"

	// expireAfterFloor keeps the entity available for at least this long, so a
	// single missed publish is not reported as a dead bridge.
	expireAfterFloor = 60 * time.Second
)

// DiscoveryPayload returns the retained Home Assistant discovery configuration
// for the load sensor.
//
// The payload describes a single measurement in watts, so Home Assistant files
// it as a power sensor with long-term statistics enabled. The availability
// topic is shared with the bridge's Last Will; expire_after covers the case
// where the process is alive but no longer publishing.
func DiscoveryPayload(cfg config.Config) string {
	payload := map[string]interface{}{
		"name":                        "Load",
		"unique_id":                   cfg.DeviceID + "_" + config.LoadObjectID,
		"default_entity_id":           fmt.Sprintf("sensor.%s_%s", cfg.DeviceID, config.LoadObjectID),
		"state_topic":                 cfg.StateTopic(),
		"availability_topic":          cfg.AvailabilityTopic(),
		"payload_available":           "online",
		"payload_not_available":       "offline",
		"unit_of_measurement":         "W",
		"device_class":                "power",
		"state_class":                 "measurement",
		"suggested_display_precision": 0,
		"expire_after":                expireAfter(cfg.PollInterval),
		"device": map[string]interface{}{
			"identifiers":  []string{cfg.DeviceID},
			"name":         cfg.DeviceName,
			"manufacturer": "CyberPower",
			"model":        cfg.DeviceModel,
		},
		"origin": map[string]string{
			"name":       "Twilight Zone UPS bridge",
			"sw_version": version,
		},
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		// This fixed map only contains JSON-serializable values.
		panic(err)
	}
	return string(encoded)
}

// expireAfter returns the number of seconds after which Home Assistant marks the
// entity unavailable when nothing new was published. It tracks the poll
// interval (four missed samples) but never drops below expireAfterFloor.
func expireAfter(interval time.Duration) int {
	window := 4 * interval
	if window < expireAfterFloor {
		window = expireAfterFloor
	}
	return int(window.Seconds())
}
