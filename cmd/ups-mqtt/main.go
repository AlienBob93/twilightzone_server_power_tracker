package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"twilightzone/ups-mqtt/internal/config"
	"twilightzone/ups-mqtt/internal/ha"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	clientOptions := mqtt.NewClientOptions().
		AddBroker(cfg.BrokerURL).
		SetClientID(cfg.ClientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetWill(cfg.AvailabilityTopic(), "offline", 1, true)

	if cfg.Username != "" {
		clientOptions.SetUsername(cfg.Username)
		clientOptions.SetPassword(cfg.Password)
	}
	clientOptions.SetOnConnectHandler(func(client mqtt.Client) {
		log.Printf("connected to MQTT broker %s", cfg.BrokerURL)
		publish(client, cfg.DiscoveryTopic(), ha.DiscoveryPayload(cfg), true)
		publish(client, cfg.AvailabilityTopic(), "online", true)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	// Publish an initial state, then periodically publish the placeholder value.
	publish(client, cfg.StateTopic(), readState(), true)
	for {
		select {
		case <-ctx.Done():
			publish(client, cfg.AvailabilityTopic(), "offline", true)
			client.Disconnect(250)
			return
		case <-ticker.C:
			publish(client, cfg.StateTopic(), readState(), true)
		}
	}
}

// readState is the hook for application logic. Replace this placeholder with
// the UPS/NUT polling and telemetry mapping you want to implement.
func readState() string {
	return "not_implemented"
}

func publish(client mqtt.Client, topic string, payload interface{}, retained bool) {
	token := client.Publish(topic, 1, retained, payload)
	if !token.WaitTimeout(5 * time.Second) {
		log.Printf("timed out publishing MQTT topic %q", topic)
		return
	}
	if err := token.Error(); err != nil {
		log.Printf("publish MQTT topic %q: %v", topic, err)
	}
}
