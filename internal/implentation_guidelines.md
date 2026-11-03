Good — you're at the right starting point. `main.go` is the last thing to change; the three files it depends on come first, and each step is independently testable. Here's the work order.

## Step 1 — `internal/pwrstat`: parser first, no I/O

New file, and the only thing you write before touching `main.go`.

`func ParseLoad(output string) (float64, error)`

Rules, straight from the spec we settled:

- Iterate lines; find the first run of **≥2 dots**; left side is the label, everything right of the dot-run is the value. Labels never contain dots, values do (`47 min.`, `Blackout at 2026/11/02 23:17:34`).
- Match the label **exactly** against `"Load"`. Not `strings.Contains` — `Rating Power` and `Utility Voltage` are why.
- Take the leading numeric run, then require the next token to start with `Watt` (case-insensitive). Ignore the parenthesised percentage entirely.
- `0 Watt(0 %)` is a **valid** result. Only a missing/unparseable `Load` is an error.

Pitfalls that will bite in tests: `Firmware Number..............` has no value at all, so the value string is empty — make sure that path returns an error rather than an index panic. And the manual's other model prints `Rating Power....515 VA (335 Watt)`, which you can add as a negative test (must not be picked up).

Test it with the verbatim output from the box as a table test, plus the six edge cases above. If `ParseLoad` is green, the hard part is done.

## Step 2 — `internal/pwrstat`: the collector

`func Collect(ctx context.Context, path string, timeout time.Duration) (float64, error)`

Details that matter here specifically:

- `exec.CommandContext` with a `context.WithTimeout` inside `Collect`, so the 5 s bound is enforced by killing the child, not by waiting.
- `cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")` — pins the labels.
- Set `cmd.Stderr` to a buffer and include it in the returned error. The daemon-down message is the only diagnostic you'll get, and `pwrstat` puts it on stderr.
- `cmd.WaitDelay` (Go 1.20+) so a stuck grandchild holding stdout can't hang `Wait` past the deadline.
- Wrap with `%w` so the next item works.

Then `IsUnusable(err error) bool` in the same package, covering `fs.ErrPermission`, `fs.ErrNotExist`, and `exec.ErrNotFound`. That's the distinction between "configured wrong, die now" and "daemon is down, keep trying".

Test the collector with a fake script written to `t.TempDir()` (`os.WriteFile(path, body, 0o755)`) — success, non-zero exit with stderr, missing file. No PATH games needed because the path is explicit.

## Step 3 — `internal/config`

Three fields plus two topic changes:

| Change | Value |
| --- | --- |
| `PWRSTAT_PATH` | default `/usr/sbin/pwrstat` |
| `PWRSTAT_TIMEOUT` | default `5`, validated `< POLL_INTERVAL` |
| `DEVICE_MODEL` | default `EC850LCD` |
| `StateTopic()` | `StatePrefix + "/load"` |
| `DiscoveryTopic()` | `%s/sensor/%s/load/config` |
| `POLL_INTERVAL` default | `15` |

The timeout/interval invariant is load-bearing: it's what makes the simple ticker in `main.go` safe (a sample can never overlap the next tick). Keep that validation.

## Step 4 — `internal/ha`

Straight field replacement, no structure changes:

| Key | Value |
| --- | --- |
| `name` | `Load` |
| `unique_id` | `DeviceID + "_load"` |
| `default_entity_id` | `sensor.` + DeviceID + `_load` |
| `unit_of_measurement` | `W` |
| `device_class` | `power` |
| `state_class` | `measurement` |
| `suggested_display_precision` | `0` |
| `expire_after` | `4 × PollInterval`, floor 60 |
| `device.manufacturer` / `model` | `CyberPower` / `cfg.DeviceModel` |

Drop the `bridge_status` naming everywhere.

## Step 5 — `cmd/ups-mqtt/main.go`

This is the only file where you're changing control flow. Keep the client setup exactly as it is; it's correct, including the `WaitTimeout` + `Error()` pair.

**a. Startup sample.** Before the loop, collect once and publish. Two reasons: the entity has a value within a second of boot, and an `IsUnusable` error lets you `log.Fatal` with a useful message instead of restart-looping. A *recoverable* error (daemon down) should just log and continue.

**b. The loop.** Your ticker is fine — and stays fine because config guarantees `PWRSTAT_TIMEOUT < POLL_INTERVAL`. If you'd rather be structurally immune to future config changes, reset a `time.Timer` after each sample instead; that's fixed-delay rather than fixed-rate. Either is defensible; don't do both.

**c. Availability state machine.** Three variables of state across iterations:

- `available bool` — start `true`, since `OnConnectHandler` already published `online`.
- `failures int` — increment per failed sample, reset to 0 on success.
- On failure: log, and only when `failures` crosses 3 and `available` is still true, publish retained `offline` and set `available = false`. No value is published.
- On success: if `!available`, publish `online` and set it true; then publish the value.

Never publish `0` or `""` for a failed sample. That's the whole point of the state machine.

**d. Payload.** Change `publish` from `interface{}` to `string` — you only ever send strings now, and it makes the call sites honest. Format with `strconv.FormatFloat(v, 'f', -1, 64)` so `66` doesn't become `66.000000`.

**e. Shutdown.** Unchanged: publish `offline`, `Disconnect(250)`, return. Keep `TimeoutStopSec` in mind — 15 s covers the publish timeout.

One thing **not** to do: don't touch `OnConnectHandler` to republish the last value. The state topic is retained, so the broker already has it, and the next tick refreshes it within 15 s. Adding shared state there means a mutex for no gain.

## Step 6 — Verify

Build and test locally first (`go test ./...` with the fixture), then push. CI will rebuild and recreate the container. To watch the actual reading without a client:

- HA → **Settings → Devices & services → MQTT → Configure → Listen to topic** `twilightzone/ups/#`, or
- watch `docker logs twilightzone-ups-mqtt` on `.46` for the connect line and any publish errors.

Two operational leftovers worth doing right after it works:

1. **Delete the ghost entity.** The deployed skeleton registered `sensor.twilightzone_ups_bridge_status` as a *retained* discovery message; it will linger forever otherwise:
   ```sh
   mosquitto_pub -h 192.168.1.46 -r -n -t 'homeassistant/sensor/twilightzone_ups/bridge_status/config'
   ```
2. **Confirm the old state topic stops updating** — `twilightzone/ups/bridge_status` will keep its last retained `not_implemented` until something clears it. Publish an empty retained payload there too if you want it tidy.

## The checklist that catches the realistic mistakes

- Parse error → **no publish**, not a zero.
- `Load` matched by exact label, not substring.
- `LC_ALL=C` in the child env — otherwise a locale change silently breaks every label match.
- Newlines stripped off the parsed number before `FormatFloat`.
- `expire_after` present, so a wedged process goes unavailable instead of showing a stale wattage forever.
- `IsUnusable` fatal at startup, but *not* fatal inside the loop.

Start with Step 1 and show me `ParseLoad` plus its tests when they're green — I'll review before you wire it into `main.go`, since every other mistake downstream is cheaper to fix there.
