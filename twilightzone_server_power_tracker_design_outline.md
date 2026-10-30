**Answer:** Yes, but with one real constraint: **the NUT daemon needs direct USB access to the EC850LCD**, which means at least one container must have a USB device passed through. The Go webapp containerizes trivially. Here are the two practical topologies.

---

## Option A — NUT on the host, Go app in Docker (recommended)

This is the cleanest split. The NUT daemon stays where the USB cable physically plugs in, and the Go app is a stateless-ish container that just talks to NUT over TCP.

```
┌──────────────────────────────────────────────────────────────┐
│  Host                                                        │
│                                                              │
│  EC850LCD ──USB──► upsd (native, port 3493)                 │
│                          ▲                                   │
│                          │ TCP 3493                          │
│  ┌────────────────────────┴────────────────────────────────┐ │
│  │  Docker container: ups-monitor (Go binary)             │ │
│  │  • queries NUT via host:3493                           │ │
│  │  • SQLite in a volume: /var/lib/ups-monitor/data.db    │ │
│  │  • serves :8080                                        │ │
│  └─────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

**Why this works well:**
- USB access is native — no `--device`, no `--privileged`, no udev gymnastics inside a container.
- NUT already listens on **TCP port 3493** by default. The Go app just connects to `host.docker.internal:3493` (or `172.17.0.1:3493` for the Docker bridge network).
- The Go app only needs read access to NUT — no special capabilities.
- SQLite persistence is one `-v` mount.

**Dockerfile for the Go app:**
```dockerfile
# ---- build stage ----
FROM golang:1.24-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /ups-monitor .

# ---- runtime stage ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /ups-monitor /usr/local/bin/ups-monitor
COPY config.yaml /etc/ups-monitor/config.yaml
VOLUME ["/var/lib/ups-monitor"]
EXPOSE 8080
ENTRYPOINT ["ups-monitor"]
```

**docker-compose.yml:**
```yaml
services:
  ups-monitor:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ups-data:/var/lib/ups-monitor
    environment:
      # Point the Go app at NUT on the host
      - NUT_HOST=host.docker.internal
      - NUT_PORT=3493
    extra_hosts:
      - "host.docker.internal:host-gateway"   # Linux: resolves to host IP
    restart: unless-stopped
    # No --device, no --privileged needed

volumes:
  ups-data:
```

**NUT config on the host** (`/etc/nut/ups.conf` + `/etc/nut/nut.conf`):
```ini
# /etc/nut/nut.conf
MAXAGE  15
DEADBATTERY 0
# Make sure upsd listens on all interfaces (or at least the Docker bridge)
LISTEN 0.0.0.0    # or omit to default to 0.0.0.0
PORT 3493
```

**Go app change:** instead of `exec.Command("upsc", "localhost")`, speak the NUT protocol over TCP directly (it's a simple text protocol):

```go
// nut.go — minimal NUT protocol client (no shell-out needed)
func (c *Collector) queryUPS() (map[string]string, error) {
    conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", c.NUTHost, c.NUTPort))
    if err != nil { return nil, err }
    defer conn.Close()

    fmt.Fprintf(conn, "SET PASSWORD monitor\n")
    fmt.Fprintf(conn, "GET UPSSTATUS\n")
    fmt.Fprintf(conn, "GET INPUT.VOLTAGE\n")
    fmt.Fprintf(conn, "GET UPS.LOAD\n")
    fmt.Fprintf(conn, "GET BATTERY.CHARGE\n")
    // read responses...
}
```

Or, if you'd rather keep shelling out, just install the `nut-client` package in the container and call `upsc host.docker.internal:3493`.

---

## Option B — Everything in Docker (two containers)

If you want the NUT daemon containerized too (e.g., you don't want native daemons on the host):

```
┌────────────────────────────────────────────────────────────────────┐
│  Docker host                                                      │
│                                                                   │
│  EC850LCD ──USB──►  ┌─────────────────────┐                      │
│                      │  Container: nut     │  --device=/dev/bus/usb/001/00X  │
│                      │  upsd + upsc       │  port 3493                     │
│                      └─────────┬───────────┘                              │
│                                │ Docker network (same subnet)             │
│                      ┌─────────▼───────────┐                              │
│                      │  Container: go-app  │  queries nut:3493           │
│                      │  Go webapp + SQLite│  volume: ups-data            │
│                      └─────────────────────┘                              │
└────────────────────────────────────────────────────────────────────────────┘
```

**docker-compose.yml:**
```yaml
services:
  nut:
    image: ghcr.io/yourorg/nut:latest   # custom image with CyberPower NUT
    # or build your own: see Dockerfile below
    devices:
      - /dev/bus/usb/001/005:/dev/bus/usb/001/005   # ← find your EC850LCD with lsusb
    # Alternative (simpler but less isolated):
    # privileged: true
    environment:
      - NUT_CONF_DIR=/etc/nut
    volumes:
      - nut-conf:/etc/nut
    ports:
      - "3493:3493"
    restart: unless-stopped

  ups-monitor:
    build: .
    depends_on:
      - nut
    ports:
      - "8080:8080"
    volumes:
      - ups-data:/var/lib/ups-monitor
    environment:
      - NUT_HOST=nut        # Docker service name, resolves on the internal network
      - NUT_PORT=3493
    restart: unless-stopped

volumes:
  nut-conf:
  ups-data:
```

**Dockerfile for the NUT container** (since CyberPower ships a `.tar`, not a package):
```dockerfile
FROM debian:bookworm-slim

# NUT client + server packages (CyberPower's tar is essentially these)
RUN apt-get update && apt-get install -y --no-install-recommends \
    nut nut-client \
    && rm -rf /var/lib/apt/lists/*

# Copy your ups.conf / upsmon.conf into the image or mount them
COPY ups.conf /etc/nut/ups.conf
COPY upsmon.conf /etc/nut/upsmon.conf
COPY nut.conf /etc/nut/nut.conf

EXPOSE 3493
CMD ["upsd", "-F"]
```

> ⚠️ **USB caveat for Option B:** You need to know the USB bus/device number (`lsusb`) and pass it with `--device`. If the USB device gets re-enumerated on reboot (different bus number), the container will break. This is the main reason **Option A is more robust** for a home setup.

---

## Comparison

| | Option A (NUT on host) | Option B (all Docker) |
|---|---|---|
| **USB access** | Native, no passthrough needed | Requires `--device` or `--privileged` |
| **USB re-enumeration on reboot** | Not a problem | Can break container |
| **Moving to a new host** | Reinstall NUT on new host | Just `docker compose up` |
| **Number of daemons on host** | 1 (upsd) + 1 container | 0 native daemons |
| **Complexity** | Lower | Higher (USB passthrough) |
| **Go app queries NUT via** | `host.docker.internal:3493` | `nut:3493` (Docker DNS) |

---

## Bottom line

- **The Go webapp containerizes cleanly** — static binary, one SQLite volume, one port. No special flags needed.
- **NUT can run in Docker** but it's the one component that touches hardware (USB). Either pass the USB device into the container (Option B) or just run it natively on the host and let the container talk to it over TCP 3493 (Option A).
- **Recommendation: Option A.** One `systemctl enable nut` on the host, one `docker compose up` for the Go app. Fewer moving parts, no USB-in-Docker fragility, and the Go app's container stays generic (no device flags, no privileged mode).
