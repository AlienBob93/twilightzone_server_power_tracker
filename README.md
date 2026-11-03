# Twilight Zone UPS load bridge — target design

> **Status: specification, not implementation.**
> The code under `cmd/` and `internal/` is still the original placeholder
> starter: it publishes a constant `not_implemented` to a `bridge_status`
> topic. This document describes what the finished bridge should do. The gaps
> are listed under [What is still to be written](#what-is-still-to-be-written).

## Target behaviour

Publish one value to Home Assistant: the UPS load in watts, read from
`pwrstat -status` on the host attached to the UPS, via MQTT discovery.

| | |
| --- | --- |
| Entity | `sensor.twilightzone_ups_load` |
| State topic | `twilightzone/ups/load` (retained, QoS 1) — e.g. `66` |
| Availability | `twilightzone/ups/availability` — `online` / `offline`, plus Last Will |
| Discovery | `homeassistant/sensor/twilightzone_ups/load/config` (retained) |
| Unit / class | `W`, `power`, `state_class: measurement` |

A scalar `state_topic` means no `value_template` and no JSON state object are
needed. With `state_class: measurement`, Home Assistant keeps long-term
statistics; for kilowatt-hours, add an Integration (Riemann sum) helper in the
UI rather than computing it here.

The full rationale, parse contract and failure model are in
[`twilightzone_server_power_tracker_design_outline.md`](twilightzone_server_power_tracker_design_outline.md).

## What the host actually looks like

Verified on the deployment host, not inferred:

| | |
| --- | --- |
| CLI | `/usr/sbin/pwrstat`, mode `0755 root:root` — world-executable |
| CLI linkage | dynamically linked, needs only `libc.so.6` (no libusb) |
| Daemon socket | `/var/pwrstatd.ipc`, mode `0755 root:root`, **path-based** |
| Socket path | **hardcoded** — `pwrstatd.conf` has no socket/ipc setting |
| Daemon unit | `pwrstatd.service`, `Restart=always`, recreates the socket on restart |

## Constraints that shape the design

- **Local only.** `pwrstat` talks to `pwrstatd` over a local socket; the CLI has
  no host/port option. The bridge must run on the machine with the USB cable.
- **Root inside the container.** Not because of the CLI — it is `0755` — but
  because of the socket: connecting to a unix socket requires *write* permission
  on its inode, and `0755 root:root` grants that only to the owner. The bridge
  therefore runs as uid 0, and the engine must be **rootful**: a rootless engine
  maps container root to a subuid and cannot write that socket.
- **Text output only.** `pwrstat` has no JSON or machine-readable mode, so the
  `Load` line is parsed. A missing or unparsable line is a failed sample, never
  zero.
- **Coarse sampling.** `pwrstatd` samples the UPS on its own cadence and serves
  cached values, so polling faster than a few seconds gains nothing and short
  dips can be invisible.

## Deploy the container

`pwrstatd` stays on the host under systemd; only the bridge is containerized.
`docker-compose.yml` bind-mounts the CLI (`/usr/sbin/pwrstat`, read-only) and the
daemon socket (`/var/pwrstatd.ipc`, read-write — `connect(2)` on a socket inside
a read-only mount returns `EROFS` on older kernels).

1. **Base image is pinned in the `Dockerfile`.** The host is Ubuntu 26.04
   (resolute) and the vendor CLI is dynamically linked against its libc, so the
   runtime stage is `FROM ubuntu:26.04` literally. Do not lower this to an older
   base or a musl image (Alpine) — the mounted binary would fail to load. It is
   deliberately not a build arg: the deploy pipeline builds the Dockerfile
   without compose `build.args`, and an unpinned value resolves to empty. The
   image has no CA bundle by default, so the `Dockerfile` installs
   `ca-certificates` when `apt-get` is present.

2. **Confirm the CLI path** and override if it moved:

   ```sh
   ls -l /usr/sbin/pwrstat
   ```

   `PWRSTAT_HOST_PATH` in `.env` defaults to `/usr/sbin/pwrstat`; inside the
   container it is always mounted at the same path.

3. **Run it** (rootful engine required):

   ```sh
   cp .env.example .env      # broker credentials + PWRSTAT_HOST_PATH if needed
   docker compose up -d --build
   docker compose logs -f
   ```

4. **Verify the socket really works from inside the container**:

   ```sh
   docker compose exec ups-mqtt /usr/sbin/pwrstat -status
   docker compose ps          # health status
   ```

**Known limitation.** `pwrstatd` is `Restart=always` and recreates
`/var/pwrstatd.ipc` when it restarts, leaving a stale inode in the container.
Every `pwrstat` call then fails until the container is restarted:

```sh
docker compose restart ups-mqtt
```

The socket path is hardcoded, so there is no clean way to mount a directory
instead. The failure is visible (entity unavailable, container unhealthy) rather
than silent, and the bridge's own availability logic marks the entity offline.

**Fallback.** If the stale-socket restarts are unacceptable, run the same binary
directly on the host under systemd — run as root, `After=pwrstatd.service`,
`EnvironmentFile=`, `Restart=on-failure`. That removes the mount, the glibc
coupling and the staleness problem. If the CLI turns out to talk to hardware
rather than to the daemon, a container cannot work at all.

## Configuration

Target configuration. Items marked *to add* do not exist in `internal/config`
yet.

| Variable | Default | Status | Purpose |
| --- | --- | --- | --- |
| `MQTT_URL` | `tcp://192.168.1.46:1883` | existing | Broker URL. |
| `MQTT_USER` / `MQTT_PASS` | empty | existing | Broker credentials; set both or neither. |
| `MQTT_CLIENT_ID` | `twilightzone-ups` | existing | MQTT client identifier. |
| `MQTT_DISCOVERY_PREFIX` | `homeassistant` | existing | Home Assistant discovery prefix. |
| `MQTT_STATE_PREFIX` | `twilightzone/ups` | existing | Prefix for the load and availability topics. |
| `DEVICE_ID` | `twilightzone_ups` | existing | Stable device identifier; also used in topics. |
| `DEVICE_NAME` | `Twilight Zone UPS` | existing | Device name shown in Home Assistant. |
| `POLL_INTERVAL` | `10` → **`15`** | change default | Seconds between samples. |
| `DEVICE_MODEL` | `EC850LCD` | to add | Device model shown in Home Assistant. |
| `PWRSTAT_PATH` | `/usr/sbin/pwrstat` | to add | Path to the CLI. |
| `PWRSTAT_TIMEOUT` | `5` | to add | Seconds per sample; must be below `POLL_INTERVAL`. |

`PWRSTAT_HOST_PATH` is a compose-only variable (default `/usr/sbin/pwrstat`); it
selects what the host path is bind-mounted into the container.

## What is still to be written

- `internal/pwrstat/` — run the CLI with a bounded context and `LC_ALL=C`, then
  parse the `Load` line. `Load......................... 66 Watt(13 %)` → `66`;
  `0 Watt(0 %)` is valid.
- `internal/config` — `PWRSTAT_PATH`, `PWRSTAT_TIMEOUT`, `DEVICE_MODEL`; rename
  the state topic to `…/load` and the discovery topic to `…/load/config`.
- `internal/ha` — discovery payload for the single `power` sensor, with the
  CyberPower device block and `expire_after`.
- `cmd/ups-mqtt` — replace `readState()` with the sample-and-publish loop:
  timeouts, fixed delay, and availability that flips to `offline` after a few
  consecutive failures.

## Migrating from the starter version

The original `sensor.twilightzone_ups_bridge_status` is retained at the broker
and will keep reappearing as a ghost entity once the code stops publishing to it.
Delete it with an empty retained payload:

```sh
mosquitto_pub -h 192.168.1.46 -r -n \
  -t 'homeassistant/sensor/twilightzone_ups/bridge_status/config'
```

Do this after the new entity is working, not before.

## Out of scope

Battery capacity, runtime, state flags, test results, outage history, thresholds
and energy accounting are deliberately not implemented. Two findings from the
hardware make some of them pointless anyway: `Test Result` never populates on
this model, and `State` was only ever observed as `Normal` — a self-test that
ran the load on battery left no trace in the status output or the daemon log.
