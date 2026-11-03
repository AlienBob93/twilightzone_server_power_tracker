package config

import (
	"errors"
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
	DeviceModel     string
	PwrstatPath     string
	PwrstatTimeout  time.Duration
	PollInterval    time.Duration
}

// LoadObjectID is the object identifier used for the UPS load sensor. It is
// used as the final segment of the state topic and in the Home Assistant
// discovery topic and object ID.
const LoadObjectID string = "load"

// Load reads configuration from environment variables, applies defaults, and
// validates the resulting settings before returning the configuration.
func Load() (Config, error) {
	cfg := Config{
		BrokerURL:       env("MQTT_URL", ""),
		Username:        os.Getenv("MQTT_USER"),
		Password:        os.Getenv("MQTT_PASS"),
		ClientID:        env("MQTT_CLIENT_ID", "twilightzone-ups"),
		DiscoveryPrefix: strings.Trim(os.Getenv("MQTT_DISCOVERY_PREFIX"), "/"),
		StatePrefix:     strings.Trim(env("MQTT_STATE_PREFIX", "twilightzone/ups"), "/"),
		DeviceID:        env("DEVICE_ID", "twilightzone_ups"),
		DeviceName:      env("DEVICE_NAME", "Twilight Zone UPS"),
		DeviceModel:     env("DEVICE_MODEL", "EC850LCD"), // Defaults to the Twilightzone's OG UPS
		PwrstatPath:     env("PWRSTAT_PATH", "/usr/sbin/pwrstat"),
	}

	if cfg.BrokerURL == "" {
		return Config{}, errors.New("MQTT_URL is required")
	}
	parsedURL, err := url.Parse(cfg.BrokerURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return Config{}, fmt.Errorf("MQTT_URL must be a broker URL, e.g. tcp://192.168.1.46:1883")
	}

	if cfg.DiscoveryPrefix == "" {
		cfg.DiscoveryPrefix = "homeassistant"
	}

	var pollIntervalErr, pwrstatTimeoutErr error
	cfg.PollInterval, pollIntervalErr = getPositiveSecondsFromEnvString("POLL_INTERVAL", "15")
	cfg.PwrstatTimeout, pwrstatTimeoutErr = getPositiveSecondsFromEnvString("PWRSTAT_TIMEOUT", "10")
	if pollIntervalErr != nil || pwrstatTimeoutErr != nil {
		return Config{}, errors.Join(pollIntervalErr, pwrstatTimeoutErr)
	}
	if cfg.PwrstatTimeout >= cfg.PollInterval {
		return Config{}, fmt.Errorf("PWRSTAT_TIMEOUT (%s) must be less than POLL_INTERVAL (%s)", cfg.PwrstatTimeout, cfg.PollInterval)
	}

	if (cfg.Username == "") != (cfg.Password == "") {
		return Config{}, fmt.Errorf("set both MQTT_USER and MQTT_PASS, or neither")
	}
	if !topicSegmentSafe(cfg.DeviceID) {
		return Config{}, fmt.Errorf("DEVICE_ID may contain only letters, digits, underscores and hyphens")
	}
	return cfg, nil
}

// getPositiveSecondsFromEnvString reads the environment variable named by key,
// interpreting it as a positive number of seconds, and returns the value as a
// time.Duration. If the variable is unset or empty, fallback is used instead.
// An error is returned if the value is not a number or is not greater than
// zero.
func getPositiveSecondsFromEnvString(key, fallback string) (time.Duration, error) {
	secondsString := env(key, fallback)
	seconds, err := strconv.ParseFloat(secondsString, 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("%s value must be a positive number of seconds", key)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func (c Config) StateTopic() string {
	return c.StatePrefix + "/" + LoadObjectID
	// ex: twilightzone/ups/load
}

func (c Config) AvailabilityTopic() string {
	return c.StatePrefix + "/availability"
}

func (c Config) DiscoveryTopic() string {
	return fmt.Sprintf("%s/sensor/%s/%s/config", c.DiscoveryPrefix, c.DeviceID, LoadObjectID)
	// ex: homeassistant/sensor/twilightzone_ups/load/config
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
