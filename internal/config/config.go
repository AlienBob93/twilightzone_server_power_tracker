package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BrokerURL       string
	Username        string
	Password        string
	ClientID        string
	DiscoveryPrefix string
	StatePrefix     string
	DeviceID        string
	DeviceName      string
	PollInterval    time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		BrokerURL:       env("MQTT_URL", "tcp://192.168.1.46:1883"),
		Username:        os.Getenv("MQTT_USER"),
		Password:        os.Getenv("MQTT_PASS"),
		ClientID:        env("MQTT_CLIENT_ID", "twilightzone-ups"),
		DiscoveryPrefix: strings.Trim(os.Getenv("MQTT_DISCOVERY_PREFIX"), "/"),
		StatePrefix:     strings.Trim(env("MQTT_STATE_PREFIX", "twilightzone/ups"), "/"),
		DeviceID:        env("DEVICE_ID", "twilightzone_ups"),
		DeviceName:      env("DEVICE_NAME", "Twilight Zone UPS"),
	}
	if cfg.DiscoveryPrefix == "" {
		cfg.DiscoveryPrefix = "homeassistant"
	}

	pollSeconds := env("POLL_INTERVAL", "10")
	seconds, err := strconv.ParseFloat(pollSeconds, 64)
	if err != nil || seconds <= 0 {
		return Config{}, fmt.Errorf("POLL_INTERVAL must be a positive number of seconds")
	}
	cfg.PollInterval = time.Duration(seconds * float64(time.Second))

	parsedURL, err := url.Parse(cfg.BrokerURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return Config{}, fmt.Errorf("MQTT_URL must be a broker URL, e.g. tcp://192.168.1.46:1883")
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return Config{}, fmt.Errorf("set both MQTT_USER and MQTT_PASS, or neither")
	}
	if !topicSegmentSafe(cfg.DeviceID) {
		return Config{}, fmt.Errorf("DEVICE_ID may contain only letters, digits, underscores and hyphens")
	}
	return cfg, nil
}

func (c Config) StateTopic() string {
	return c.StatePrefix + "/bridge_status"
}

func (c Config) AvailabilityTopic() string {
	return c.StatePrefix + "/availability"
}

func (c Config) DiscoveryTopic() string {
	return fmt.Sprintf("%s/sensor/%s/bridge_status/config", c.DiscoveryPrefix, c.DeviceID)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func topicSegmentSafe(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
