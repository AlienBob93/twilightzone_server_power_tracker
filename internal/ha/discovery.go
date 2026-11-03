package ha

import (
	"encoding/json"

	"twilightzone/ups-mqtt/internal/config"
)

// DiscoveryPayload returns the retained configuration for the starter sensor.
func DiscoveryPayload(cfg config.Config) string {
	payload := map[string]interface{}{
		"name":                  "Bridge status",
		"unique_id":             cfg.DeviceID + "_bridge_status",
		"default_entity_id":     "sensor.twilightzone_ups_bridge_status",
		"state_topic":           cfg.StateTopic(),
		"availability_topic":    cfg.AvailabilityTopic(),
		"payload_available":     "online",
		"payload_not_available": "offline",
		"device": map[string]interface{}{
			"identifiers":  []string{cfg.DeviceID},
			"name":         cfg.DeviceName,
			"manufacturer": "Twilight Zone",
			"model":        "UPS MQTT bridge",
		},
		"origin": map[string]string{
			"name":       "Twilight Zone UPS bridge",
			"sw_version": "0.1.0",
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// This fixed map only contains JSON-serializable values.
		panic(err)
	}
	return string(encoded)
}
