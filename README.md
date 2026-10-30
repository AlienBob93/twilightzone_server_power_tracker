# Twilight Zone server power tracker

A small UPS telemetry collector, SQLite time-series store, JSON API and
dashboard. It reads a CyberPower EC850LCD (or any NUT-supported UPS) through
the Network UPS Tools daemon and shows voltage, load, battery and runtime
history.

This repository implements **Option A** from
[`twilightzone_server_power_tracker_design_outline.md`](twilightzone_server_power_tracker_design_outline.md):

- **NUT runs natively on the host**, where the USB cable is plugged in.
- **The Go application runs in Docker** and only talks to `upsd` over TCP 3493.
- No `--device`, no `--privileged`, and no USB re-enumeration fragility.

```
┌──────────────────────────────────────────────────────────────┐
│  Host                                                        │
│                                                              │
│  EC850LCD ──USB──► upsd (native, port 3493)                 │
│                          ▲                                   │
│                          │ TCP 3493                          │
│  ┌────────────────────────┴────────────────────────────────┐ │
│  │  Docker container: ups-monitor                          │ │
│  │  • queries NUT at host.docker.internal:3493             │ │
│  │  • SQLite volume: /var/lib/ups-monitor/data.db          │ │
│  │  • serves the dashboard + API on :8080                  │ │
│  └─────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

## Features

- Speaks the NUT `upsd` text protocol directly — no `upsc` shell-out needed.
- Polls every 10 seconds by default and stores structured samples in SQLite.
- Estimates real power draw from `ups.load` and the UPS's nominal wattage.
- JSON API for status, time-series samples and window statistics.
- Embedded, dependency-free dashboard with auto-refresh and canvas charts.
- Automatic retention pruning (30 days by default).
- Works with a blank UPS name by auto-detecting the first UPS `upsd` exposes.
- Static, CGO-free binary in a small Alpine container.

## Repository layout

```
cmd/ups-monitor/       application entrypoint and graceful shutdown
internal/api/          HTTP routes, JSON API and dashboard rendering
internal/collector/    polling loop and NUT variable mapping
internal/config/       YAML + environment configuration
internal/model/        shared sample and status types
internal/nut/          NUT upsd protocol client
internal/store/        SQLite schema, queries and retention
deploy/nut/            host-side NUT config for the EC850LCD
web/                   embedded template, CSS and charting JavaScript
config.yaml            container configuration baked into the image
Dockerfile             multi-stage, CGO-free build
docker-compose.yml     Option A topology (host NUT + app container)
LICENSE                GNU General Public License v3.0
worklog.md             implementation and validation notes
```

## Work log

See [`worklog.md`](worklog.md) for the implementation and validation notes.

## Quick start

### 1. Configure NUT on the host

Follow [`deploy/nut/README.md`](deploy/nut/README.md). In short, on a
Debian/Ubuntu host:

```sh
sudo apt install nut
sudo cp -a /etc/nut /etc/nut.bak
sudo install -o root -g nut -m 0640 \
  deploy/nut/ups.conf deploy/nut/nut.conf deploy/nut/upsd.conf \
  deploy/nut/upsd.users deploy/nut/upsmon.conf /etc/nut/
sudo systemctl enable --now nut-server
upsc ec850lcd
```

`upsc ec850lcd` should print the UPS variables. `upsd` now listens on
`0.0.0.0:3493`.

> `upsd.users` and `upsmon.conf` are only needed for host shutdown handling.
> UPS Monitor reads variables without authentication.

### 2. Start the container

```sh
docker compose up -d --build
```

Open <http://localhost:8080>. On Linux, `extra_hosts` maps
`host.docker.internal` to the host gateway, so the container can reach the
native `upsd` without USB passthrough.

To override defaults, copy `.env.example` to `.env`:

```sh
cp .env.example .env
# NUT_HOST, NUT_PORT, NUT_UPS, LOG_LEVEL
docker compose up -d --build
```

## Local development (no Docker)

With NUT running on the same machine, the built-in defaults are enough:

```sh
go run ./cmd/ups-monitor
# dashboard: http://localhost:8080
# database:  ./data/ups-monitor.db
# NUT:       localhost:3493
```

Useful overrides:

```sh
NUT_HOST=192.168.1.10 POLL_INTERVAL=5s LOG_LEVEL=debug \
  go run ./cmd/ups-monitor
```

Requires Go 1.27+ (see `go.mod`). The SQLite driver is pure Go, so no C
toolchain is needed.

To mimic the container configuration locally:

```sh
DB_PATH=./data/ups-monitor.db NUT_HOST=localhost \
  go run ./cmd/ups-monitor -config ./config.yaml
```

## Configuration

`-config` is optional. When omitted, built-in defaults and environment
variables are used. The Docker image passes `/etc/ups-monitor/config.yaml`.

```yaml
server:
  address: "0.0.0.0:8080"
  refresh_seconds: 10      # dashboard auto-refresh interval

nut:
  host: "host.docker.internal"
  port: 3493
  ups: ""                  # empty or "auto" = first UPS on the server
  username: ""             # optional; only for restricted upsd setups
  password: ""
  timeout: 5s

collector:
  interval: 10s

storage:
  path: "/var/lib/ups-monitor/data.db"
  retention: 720h          # 30 days; 0 disables pruning

logging:
  level: "info"            # debug | info | warn | error
  format: "text"           # text | json
```

### Environment variables

Environment variables override the YAML file.

| Variable | Default | Description |
| --- | --- | --- |
| `NUT_HOST` | `localhost` | Host running `upsd` |
| `NUT_PORT` | `3493` | `upsd` TCP port |
| `NUT_UPS` | empty (auto) | UPS name, e.g. `ec850lcd` |
| `NUT_USERNAME` | empty | Optional `upsd` username |
| `NUT_PASSWORD` | empty | Optional `upsd` password |
| `NUT_TIMEOUT` | `5s` | Connect/read timeout |
| `POLL_INTERVAL` | `10s` | Collection interval (minimum 1s) |
| `HTTP_ADDR` | `0.0.0.0:8080` | HTTP listen address |
| `REFRESH_SECONDS` | `10` | Dashboard refresh interval |
| `DB_PATH` | `./data/ups-monitor.db` | SQLite database path |
| `RETENTION` | `720h` | Delete samples older than this; `0` disables |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `text` | `text` or `json` |

## Collected variables

| NUT variable | Stored field |
| --- | --- |
| `ups.status` | `status`, `on_battery`, `low_battery` |
| `input.voltage` | `input_voltage` |
| `output.voltage` | `output_voltage` |
| `input.frequency` | `input_frequency` |
| `output.frequency` | `output_frequency` |
| `ups.load` | `load_percent` |
| `battery.charge` | `battery_charge` |
| `battery.runtime` | `battery_runtime_seconds` |
| `battery.voltage` | `battery_voltage` |
| `ups.temperature` / `battery.temperature` | `temperature_c` |
| `ups.realpower.nominal` / `ups.power.nominal` | `nominal_power_w` |
| derived | `estimated_power_w` = `load_percent / 100 × nominal_power_w` |

If a UPS does not expose a variable, the field is `null`/omitted. The raw
`ups.*`, `input.*`, `output.*`, `battery.*` and `device.*` variables are stored
as JSON alongside each sample for future metrics.

> `estimated_power_w` assumes the nominal value is in watts. If NUT only exposes
> `ups.power.nominal` (VA), the estimate will read high.

## HTTP API

| Endpoint | Description |
| --- | --- |
| `GET /` | Embedded dashboard |
| `GET /api/health` | Liveness + database check |
| `GET /api/status` | Latest sample, age, stale flag, collector health |
| `GET /api/samples` | Time-series samples |
| `GET /api/stats` | Min/avg/max for the window |
| `GET /api/ups` | Live `LIST UPS` from NUT |

`/api/samples` query parameters:

- `hours=24` (default) — window ending now
- `since=2026-10-29T00:00:00Z` / `until=...` — RFC3339 or Unix seconds
- `limit=2000` — 1 to 10000 rows, newest rows win

`/api/stats` accepts `hours`, `since` and `until`.

Examples:

```sh
curl -s localhost:8080/api/status | jq
curl -s 'localhost:8080/api/samples?hours=6&limit=500' | jq '.count'
curl -s 'localhost:8080/api/stats?hours=24' | jq '.stats.metrics.input_voltage'
```

Sample response from `/api/status`:

```json
{
  "sample": {
    "id": 42,
    "timestamp": "2026-10-29T20:00:00Z",
    "ups": "ec850lcd",
    "status": "OL CHRG",
    "input_voltage": 230.1,
    "output_voltage": 229.8,
    "load_percent": 21,
    "battery_charge": 100,
    "battery_runtime_seconds": 1800,
    "nominal_power_w": 510,
    "estimated_power_w": 107.1,
    "on_battery": false,
    "low_battery": false
  },
  "age_seconds": 3.2,
  "stale": false,
  "stale_after_seconds": 30,
  "collector": {
    "last_success": "2026-10-29T20:00:00Z",
    "ups": "ec850lcd"
  }
}
```

## Storage and retention

SQLite is opened in WAL mode with a single writer. The schema is created
automatically:

- `samples` — one row per poll, timestamps stored as Unix milliseconds
- `idx_samples_ts` — window queries
- `idx_samples_ups_ts` — per-UPS history

Old rows are pruned at startup and once per hour according to `storage.retention`.
Set it to `0` to keep everything.

Back up the database by copying the file while the container is stopped, or use
SQLite's online backup tooling:

```sh
# The volume is named <compose-project>_ups-data (the directory name by default).
docker volume ls | grep ups-data
docker compose stop ups-monitor
docker run --rm \
  -v twilightzone_server_power_tracker_ups-data:/data \
  -v "$PWD":/backup \
  alpine tar czf /backup/ups-data.tar.gz -C /data .
docker compose start ups-monitor
```

## Operations

```sh
docker compose logs -f          # follow logs
docker compose restart          # restart the app
docker compose down             # stop (volume is kept)
docker compose down -v          # stop and delete the SQLite volume
make test                       # run all unit/integration tests
```

Behavior when NUT is unavailable: the collector retries every
`collector.interval`, logs the first failure (and then at debug level), keeps
serving the last samples, and the dashboard reports a stale/collector-error
state. Nothing crashes.

## Security notes

- `upsd` speaks a plaintext protocol with no TLS. Restrict TCP 3493 to the
  Docker bridge (or your monitoring host) with a firewall; see
  [`deploy/nut/README.md`](deploy/nut/README.md).
- The dashboard and API have **no authentication**. Do not expose port 8080 to
  the public internet. Bind it to a private interface or place it behind an
  authenticating reverse proxy.
- NUT credentials are optional and only needed if `upsd` restricts variable
  reads. Pass them through `NUT_USERNAME`/`NUT_PASSWORD` rather than baking
  them into an image.
- The container runs as the non-root `ups` user.

## Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| Dashboard says "No recent data" | The collector cannot reach `upsd`; check `NUT_HOST` and port 3493 |
| `connection refused` in logs | `upsd` is not running, or `LISTEN` is wrong |
| `Unknown UPS` in logs | Set `NUT_UPS` to the section name from `ups.conf` |
| Voltage/load fields are blank | The driver does not expose those variables |
| No USB access on the host | Check NUT udev rules and `usbhid-ups` status |
| Container cannot reach the host | Confirm `extra_hosts` and the host firewall |
| Old data never disappears | `storage.retention` is `0`, or pruning has not run yet |

Run `upsc ec850lcd` on the host as the ground truth; if that fails, fix NUT
before debugging the container.

## License

This project is licensed under the **GNU General Public License v3.0**
(`GPL-3.0-only`). See [`LICENSE`](LICENSE) for the full text.

Copyright (C) 2026 Prashant Rao.
