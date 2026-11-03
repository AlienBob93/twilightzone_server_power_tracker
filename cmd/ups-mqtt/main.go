package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"twilightzone/ups-mqtt/internal/config"
	"twilightzone/ups-mqtt/internal/ha"
	"twilightzone/ups-mqtt/internal/pwrstat"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	PayloadAvailable    = "online"
	PayloadNotAvailable = "offline"
)

// offlineThreshold is the number of consecutive failed reads before the
// entity is marked unavailable.
const offlineThreshold = 3

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	initialPayload, failures := "", 0
	var available atomic.Bool
	available.Store(true)
	if payload, err := readLoad(ctx, cfg); err != nil {
		if pwrstat.IsUnusable(err) {
			log.Fatalf("pwrstat is not usable: %v", err)
		}
		if ctx.Err() != nil { // we are shutting down, not failing
			return
		}
		log.Printf("initial pwrstat read failed: %v", err)
		failures = 1
	} else {
		initialPayload = payload
	}

	clientOptions := mqtt.NewClientOptions().
		AddBroker(cfg.BrokerURL).
		SetClientID(cfg.ClientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5*time.Second).
		SetKeepAlive(30*time.Second).
		SetWill(cfg.AvailabilityTopic(), PayloadNotAvailable, 1, true)

	if cfg.Username != "" {
		clientOptions.SetUsername(cfg.Username)
		clientOptions.SetPassword(cfg.Password)
	}
	clientOptions.SetOnConnectHandler(func(client mqtt.Client) {
		log.Printf("connected to MQTT broker %s", cfg.BrokerURL)

		publish(client, cfg.DiscoveryTopic(), ha.DiscoveryPayload(cfg), true)
		status := PayloadAvailable
		if !available.Load() {
			status = PayloadNotAvailable
		}
		publish(client, cfg.AvailabilityTopic(), status, true)
	})
	clientOptions.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		log.Printf("MQTT connection lost: %v", err)
	})

	client := mqtt.NewClient(clientOptions)
	connectToken := client.Connect()
	if !connectToken.WaitTimeout(15 * time.Second) {
		log.Fatal("timed out connecting to MQTT broker")
	}
	if err := connectToken.Error(); err != nil {
		log.Fatalf("connect to MQTT broker: %v", err)
	}

	shutdown := func() {
		publish(client, cfg.AvailabilityTopic(), PayloadNotAvailable, true)
		client.Disconnect(250)
	}

	if initialPayload != "" {
		publish(client, cfg.StateTopic(), initialPayload, true)
	}
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown
			shutdown()
			return
		case <-ticker.C:
			payload, err := readLoad(ctx, cfg)
			if err != nil {
				if ctx.Err() != nil {
					// Shutdown, not a UPS failure
					shutdown()
					return
				}

				failures++
				log.Printf("pwrstat read failed (%d/%d): %v", failures, offlineThreshold, err)

				if failures >= offlineThreshold && available.Load() {
					log.Printf("UPS unreadable after %d attempts: marking entity offline", failures)
					publish(client, cfg.AvailabilityTopic(), PayloadNotAvailable, true)
					available.Store(false)
				}
				continue
			}
			failures = 0
			publish(client, cfg.StateTopic(), payload, true)
			if !available.Load() {
				log.Printf("UPS reachable again: marking entity online")
				publish(client, cfg.AvailabilityTopic(), PayloadAvailable, true)
				available.Store(true)
			}
		}
	}
}

// readLoad runs pwrstat once and returns the load in watts, already formatted
// for the MQTT payload.
func readLoad(ctx context.Context, cfg config.Config) (string, error) {
	watts, err := pwrstat.Collect(ctx, cfg.PwrstatPath, cfg.PwrstatTimeout)
	if err != nil {
		return "", err
	}
	return strconv.FormatFloat(watts, 'f', -1, 64), nil
}

func publish(client mqtt.Client, topic string, payload string, retained bool) {
	token := client.Publish(topic, 1, retained, payload)
	if !token.WaitTimeout(5 * time.Second) {
		log.Printf("timed out publishing MQTT topic %q", topic)
		return
	}
	if err := token.Error(); err != nil {
		log.Printf("publish MQTT topic %q: %v", topic, err)
	}
}
