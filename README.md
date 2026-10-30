# Twilight Zone UPS MQTT starter

A deliberately small Go starter for publishing a device to Home Assistant via
MQTT Discovery. It connects to the existing Eclipse MQTT broker at
`192.168.1.46:1883`, registers one placeholder sensor, and publishes retained
state and availability.

**UPS/NUT polling and all other business logic are intentionally left for you.**
Implement that in `cmd/ups-mqtt/main.go`'s `readState()` hook and add the
entities you need in `internal/ha/discovery.go`.

## What is included

- MQTT 3.1.1 client using Eclipse Paho for Go.
- One retained Home Assistant MQTT Discovery sensor:
  `sensor.twilightzone_ups_bridge_status`.
- Retained state topic: `twilightzone/ups/bridge_status`.
- Availability topic and Last Will: `twilightzone/ups/availability`.
- Minimal Docker image and Compose file. No broker, web server, NUT client,
  database, or telemetry/business logic is included.

The broker/discovery pattern is based on the existing
`/home/alienbob/Documents/Projects/truenas-gpu-stats` integration. The default
discovery prefix is `homeassistant`; the distinct state prefix and device ID
avoid colliding with that integration.

## Run with Docker Compose

1. Copy `.env.example` to `.env` and fill in the MQTT credentials used by the
   broker. Leave both `MQTT_USER` and `MQTT_PASS` empty if anonymous access
   is configured.
   The account must be allowed to publish retained messages under
   `homeassistant/sensor/twilightzone_ups/#` and `twilightzone/ups/#`; the
   existing GPU integration's account may have narrower ACLs.
2. Make sure Home Assistant's MQTT integration is connected to
   `192.168.1.46:1883` with discovery enabled and prefix `homeassistant`.
3. Start the publisher:

   ```sh
   docker compose up -d --build
   ```

The device should appear under **Settings → Devices & services → MQTT**.

## Run locally

```sh
cp .env.example .env
set -a
. ./.env
set +a
go run ./cmd/ups-mqtt
```

Requires Go 1.23 or later.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `MQTT_URL` | `tcp://192.168.1.46:1883` | Existing MQTT broker URL. |
| `MQTT_USER` / `MQTT_PASS` | empty | Broker credentials. Set both or neither. |
| `MQTT_CLIENT_ID` | `twilightzone-ups` | MQTT client identifier. |
| `MQTT_DISCOVERY_PREFIX` | `homeassistant` | HA MQTT discovery prefix. |
| `MQTT_STATE_PREFIX` | `twilightzone/ups` | Prefix for state/availability topics. |
| `DEVICE_ID` | `twilightzone_ups` | Stable HA device identifier. |
| `DEVICE_NAME` | `Twilight Zone UPS` | Device name shown in HA. |
| `POLL_INTERVAL` | `10` | Seconds between placeholder state publishes. |

## Project layout

```text
cmd/ups-mqtt/        process lifecycle, MQTT connection, business-logic hook
internal/config/     environment configuration
internal/ha/         starter Home Assistant discovery payload
```

See [`twilightzone_server_power_tracker_design_outline.md`](twilightzone_server_power_tracker_design_outline.md)
for the intentionally limited starter scope.
