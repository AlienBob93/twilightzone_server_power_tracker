# Twilight Zone UPS MQTT bridge — starter outline

## Scope

This is a **barebones Go Home Assistant MQTT Discovery starter**, not a
finished UPS integration. It connects to the existing Eclipse MQTT broker at
`192.168.1.46:1883`, registers one placeholder HA sensor, and publishes a
retained placeholder state.

UPS/NUT polling, telemetry mapping, energy calculations, and all other
business logic are intentionally not implemented. Add them in
`cmd/ups-mqtt/main.go:readState()` and add the corresponding HA entities in
`internal/ha/discovery.go`.

## Topology

```text
UPS / future data source -> Go publisher -> Eclipse MQTT 192.168.1.46:1883
                                                  |
                                                  +-> Home Assistant MQTT
                                                      Discovery integration
```

The broker is external and is not started or configured by this project. The
existing project at `/home/alienbob/Documents/Projects/truenas-gpu-stats` uses
the same broker and discovery prefix. This starter uses its own state topic
base (`twilightzone/ups`) and stable device identifier (`twilightzone_ups`) to
avoid collisions.

## Included behavior

- MQTT 3.1.1 client using Eclipse Paho for Go.
- Retained discovery configuration for one placeholder status sensor.
- Retained placeholder state on `twilightzone/ups/bridge_status`.
- Retained availability and Last Will on `twilightzone/ups/availability`.
- Environment-based broker configuration; no broker credentials in source.
- Docker Compose runs only the publisher and exposes no inbound ports.

## Files

| File | Purpose |
| --- | --- |
| `cmd/ups-mqtt/main.go` | MQTT lifecycle, placeholder publish loop, business-logic hook. |
| `internal/config/config.go` | Broker, topic, device and interval configuration. |
| `internal/ha/discovery.go` | Placeholder entity discovery payload. |
| `go.mod` | Go module and MQTT dependency. |
| `Dockerfile`, `docker-compose.yml` | Minimal container deployment. |
| `.env.example` | Local configuration template. |

## Run

1. Copy `.env.example` to `.env` and add the broker credentials if required.
2. Ensure Home Assistant's MQTT integration is connected to the same broker
   and uses the `homeassistant` discovery prefix.
3. Run `docker compose up -d --build`.
4. The placeholder device appears in Home Assistant. Add telemetry logic and
   entities as needed.

There is no broker setup, NUT client, HTTP server, database, UPS entity
catalog, or application-specific business logic in this starter.
