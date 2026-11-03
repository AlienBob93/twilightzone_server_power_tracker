# Twilight Zone UPS load bridge — design record

> **Status: implemented and deployed.** This file is the decision record; the
> code it describes lives in `cmd/ups-mqtt` and `internal/`. Where the
> implementation settled a detail this plan left open — the ticker, the timeout
> default, the atomic availability flag — the delivered behaviour is noted
> inline and summarised under [As built](#as-built).

## Scope

Publish one value to Home Assistant: the UPS load in watts, read from
`pwrstat -status` on the host attached to the UPS, over MQTT discovery.

Everything else the UPS reports is deliberately out of scope — battery capacity,
runtime, state flags, test results, outage history, thresholds, energy totals.

## Topology

```text
UPS (USB HID) <- pwrstatd (host, root, systemd) <- pwrstat CLI (0755; the socket needs root)
                                                          ^
                                                          | bind-mounted socket
                                                    [container: ups-mqtt] -> MQTT -> Home Assistant
```

## Decisions

### Data source: `pwrstat`, on the host, as root

`pwrstat` is the CLI of CyberPower PowerPanel for Linux, a thin client for the
`pwrstatd` daemon over a local endpoint. Consequences:

- **No remote mode.** There is no host/port option, so the bridge cannot run on a
  different machine from the UPS. A container is only viable if it shares the
  host's IPC endpoint or network namespace — see "Container deployment" below.
- **Root required, but not for the reason first assumed.** On the deployment host
  the CLI is `/usr/sbin/pwrstat` at mode `0755` — world-executable, so a non-root
  process can run it. What needs root is the daemon socket: `/var/pwrstatd.ipc`
  is `0755 root:root`, and `connect(2)` on a unix socket requires *write*
  permission on the inode, which only the owner holds. The container therefore
  runs as uid 0 and the engine must be rootful; a rootless engine maps container
  root to a subuid and cannot write that socket.

  (Earlier notes in this project claimed the CLI was mode `0700` and only
  root-executable. Those observations came from the workstation, not from the
  deployment host, and do not apply here.)
  Weakening the socket mode is not an option: `pwrstatd` recreates it on restart.
- **No machine-readable output.** The complete option list (`-help -version
  -config -status -reset -alarm -mute -test -hibernate -pwrfail -lowbatt -cloud
  -verify`) contains no JSON/CSV mode, so one text field is parsed.

### Parse contract

Only the `Load` line is read:

```text
		Load......................... 66 Watt(13 %)
```

- Split each line at the first run of two or more dots; the left side is the
  label, the right side the value. Labels contain no dots; values may
  (`45 min.`, `2022/03/15 09:35:35`), so the split happens once, at the leader.
- Match the label exactly (`Load`), never by substring — `Rating Power` and
  `Utility Voltage`/`Output Voltage` would cross-wire otherwise.
- Take the leading number, then require the unit token to start with `Watt`.
  Ignore the parenthesised percentage.
- A load of `0 W` is a valid reading and is published. Absent or unparsable
  values are errors, never zero.
- Pin `LC_ALL=C`/`LANG=C` for the subprocess so labels stay English.

Only `Load` is required for a successful sample; the vendor documents that other
fields appear only when the model supports them (`-status` note in the manual).

### Sampling

`pwrstatd` polls the UPS on its own schedule and serves cached values, so the CLI
is a sampled snapshot, not an event stream. A self-test that ran the load on
battery left `State` at `Normal`, `Test Result` at `Unknown`, and no log entry —
short transitions can be missed entirely. Default `POLL_INTERVAL` is therefore
15 s, and no automation should treat this bridge as a transient-power detector.

The loop is implemented with a fixed-rate `time.Ticker`, not the fixed-delay
timer considered here. That is safe because `config` enforces
`PWRSTAT_TIMEOUT < POLL_INTERVAL` and the collector adds a 2 s `cmd.WaitDelay`:
at the defaults (10 s timeout, 15 s interval) the worst case is 12 s of a 15 s
interval, so a slow sample cannot overlap the next tick. Each sample is bounded
by `PWRSTAT_TIMEOUT` through the command context, which kills the child rather
than waiting for it.

### Topics and entity

| Piece | Value |
| --- | --- |
| State | `twilightzone/ups/load`, retained, QoS 1, payload is the bare number |
| Availability | `twilightzone/ups/availability`, `online`/`offline`, plus Last Will |
| Discovery | `homeassistant/sensor/twilightzone_ups/load/config`, retained |

Entity: `sensor.twilightzone_ups_load`, `device_class: power`,
`state_class: measurement`, `unit_of_measurement: W`,
`expire_after: 4 × POLL_INTERVAL` (minimum 60 s).

### Failure handling

| Failure | Detection | Behaviour |
| --- | --- | --- |
| Binary missing or not executable | `fs.ErrNotExist` / `fs.ErrPermission` / `exec.ErrNotFound` | Fatal at startup: the CLI cannot be executed at all. Never fatal in the loop. |
| Daemon not running | non-zero exit, stderr text | Logged; after 3 consecutive failures publish retained `offline`. |
| Output drift (no `Load` line) | `ErrFieldMissing` / `ErrValueUnparseable` | Counts as a failed sample; no value is published. |
| Sample times out | `context.DeadlineExceeded` | Counts as a failed sample; retryable. |
| Bridge stopping | `context.Canceled` while `ctx.Err() != nil` | Not counted as a UPS failure; clean `offline` publish and disconnect. |
| Process hang | no publish | `expire_after` makes the entity unavailable. |
| Process kill / reboot | connection drops | Broker publishes the retained Last Will. |

### Container deployment

`pwrstatd` stays on the host; only the bridge is containerized. The CLI
(`/usr/sbin/pwrstat`, `0755`) is bind-mounted read-only rather than vendored into
the image.

The daemon listens on a **path-based** unix socket, `/var/pwrstatd.ipc`
(`0755 root:root`), verified via `ss -lxp`. Consequences:

- `network_mode: host` does not help — host networking shares abstract sockets
  and TCP, not the filesystem.
- The socket's parent directory is `/var`, which must not be handed to a root
  container, so the socket is mounted as a file, read-write (a socket inside a
  read-only mount returns `EROFS` from `connect(2)` on older kernels).
- `pwrstatd.conf` exposes no socket-path option, so the location is fixed and
  cannot be moved to a mountable directory.

`pwrstatd` is `Restart=always` and recreates the socket on restart, leaving a
stale inode in the container. The bridge then reports the entity unavailable
until the container is restarted. This is the main cost of containerising; the
failure is visible rather than silent.

The image's glibc must be at least as new as the host's, because the CLI is
dynamically linked against libc (only `libc.so.6`, no libusb). The host is
Ubuntu 26.04, so the `Dockerfile` runtime stage is `FROM ubuntu:26.04` literally
rather than through a build arg — the deploy pipeline builds without compose
`build.args`, and a blank value breaks `FROM`. A musl base or an older
Ubuntu/Debian would fail to load the mounted binary.

Running the same binary on the host under systemd removes the mount, the glibc
coupling and the staleness problem entirely.

### HA-side, not Go-side

Energy accounting belongs in Home Assistant: a Riemann-sum helper over a
`measurement` power sensor with `state_class` produces kWh and survives restarts,
whereas a counter in this process would need its own persistence.

## Implementation plan

| Path | Status | Purpose |
| --- | --- | --- |
| `internal/pwrstat/` | done, tested | CLI invocation, `Load` parsing, error classification (`ErrFieldMissing`, `ErrValueUnparseable`, `IsUnusable`). |
| `internal/config/` | done, tested | `PWRSTAT_PATH`, `PWRSTAT_TIMEOUT`, `DEVICE_MODEL`, `LoadObjectID`; topics retargeted to `…/load`; device-id and duration validation. |
| `internal/ha/` | done, tested | Single-sensor discovery payload. |
| `cmd/ups-mqtt/` | done, `readLoad` tested | Startup probe, sample loop, availability state machine, shutdown. |
| `Dockerfile`, `docker-compose.yml` | done | Container build and deployment scaffold. |
| `.github/workflows/deploy.yml` | done | `test` job (`go vet`, `go test -race`) gates the Dockhand deploy job. |

## As built

Details the plan left open, settled during implementation:

| | |
| --- | --- |
| Defaults | `POLL_INTERVAL=15`, `PWRSTAT_TIMEOUT=10`, `PWRSTAT_PATH=/usr/sbin/pwrstat`, `DEVICE_MODEL=EC850LCD`, discovery prefix `homeassistant`. `MQTT_URL` has no default and is required. |
| Loop | Fixed-rate `time.Ticker`, made safe by `timeout + 2 s WaitDelay < interval`. |
| Offline threshold | 3 consecutive failed samples (`offlineThreshold` in `cmd/ups-mqtt`). |
| Availability | `sync/atomic.Bool`, because `OnConnectHandler` runs on paho's network goroutine and must republish the current state after a broker restart. |
| Startup | One sample before the MQTT connect; `IsUnusable` exits non-zero, other errors start the counter at 1 and publish nothing. |
| Payload | `strconv.FormatFloat(w, 'f', -1, 64)` — `66` stays `66`, `66.5` stays `66.5`. |
| `expire_after` | `4 × POLL_INTERVAL` seconds, floored at 60. |
| Ghost topics | The starter's `bridge_status` discovery config and state topic were deleted with empty retained payloads. |

## Rejected alternatives

| Option | Why not |
| --- | --- |
| NUT (`upsc`/`upsd`) | Not what runs on this host; would also contend for the same USB HID device. |
| Container running the vendor daemon | A second daemon would fight the host's `pwrstatd` for `/dev/hidraw*`, and the host daemon is what performs UPS-initiated shutdown. |
| Container on a different host | `pwrstat` has no remote mode. |
| Direct HID/USB polling | Reimplements the vendor protocol for one number. |
| Vendoring the CLI into the image | Proprietary blob, must be refreshed on every vendor update; a read-only bind mount achieves the same with less churn. |
| `pwrstat -config` / `/var/log/pwrstatd.log` | Out of scope now; the log is the only place sub-interval events would appear, if that is ever needed. |
| Full entity catalog (capacity, runtime, state, test result) | Requested scope is the load only. `Test Result` never populates on this model, and `State` was observed only as `Normal`. |
