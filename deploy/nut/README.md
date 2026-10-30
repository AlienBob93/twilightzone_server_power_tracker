# Host-side NUT setup (Option A)

In Option A the NUT daemon runs **natively on the Docker host**, because that is
where the EC850LCD's USB cable is plugged in. Only the Go application runs in
Docker; it reaches `upsd` over TCP 3493.

```
EC850LCD ──USB──► upsd (native, :3493)
                        ▲
                        │ TCP 3493
              ┌─────────┴──────────┐
              │ container:         │
              │ ups-monitor :8080  │
              └────────────────────┘
```

## 1. Install NUT on the host

```sh
# Debian / Ubuntu
sudo apt install nut
# Fedora / RHEL
sudo dnf install nut
# Arch
sudo pacman -S nut
```

The NUT packages include the `usbhid-ups` driver used by CyberPower devices.

## 2. Install the configuration

Back up the package defaults first, then copy the files from this directory:

```sh
sudo cp -a /etc/nut /etc/nut.bak
sudo install -o root -g nut -m 0640 ups.conf nut.conf upsd.conf upsd.users upsmon.conf /etc/nut/
```

`nut.conf` uses `MODE=netserver` because the UPS is shared with the container
over TCP. `upsd.users` and `upsmon.conf` are only needed if you also want upsmon
to shut the host down on low battery. If you skip them, UPS Monitor still works.

If your distribution uses a different group for NUT, adjust `-g nut`.

## 3. Start NUT

```sh
# Debian / Ubuntu
sudo systemctl enable --now nut-server
# optional host-shutdown monitoring:
sudo systemctl enable --now nut-monitor

# Fedora
sudo systemctl enable --now nut-server nut-monitor
```

Newer NUT packages generate a driver unit per UPS section. If no driver was
started automatically:

```sh
sudo upsdrvctl start
sudo systemctl restart nut-server
```

Check that the driver and server are happy:

```sh
systemctl status nut-server
sudo upsdrvctl status
```

## 4. Verify locally

```sh
upsc ec850lcd
upsc ec850lcd input.voltage ups.load battery.charge ups.status
```

You should see variables such as `input.voltage: 230.0`, `ups.status: OL`, and
`battery.charge: 100`.

## 5. Verify over TCP

From the host itself:

```sh
upsc ec850lcd@127.0.0.1:3493
```

From another machine (replace with the host's address):

```sh
upsc ec850lcd@192.168.1.10:3493
```

If that fails, check `LISTEN` in `/etc/nut/upsd.conf` and the firewall.

## 6. Start the container

From the project root:

```sh
docker compose up -d --build
```

The compose file maps `host.docker.internal` to the host gateway, so the
container can reach the native `upsd`. No USB passthrough, no `--privileged`,
and no `/dev/bus/usb` bind mount is used.

Open <http://localhost:8080>.

## Firewall

`upsd` has no encryption. Restrict port 3493 to the Docker bridge network or
your LAN:

```sh
# ufw example (Docker's default bridge)
sudo ufw allow from 172.17.0.0/16 to any port 3493 proto tcp

# nftables example
sudo nft add rule inet filter input ip saddr 172.17.0.0/16 tcp dport 3493 accept
```

Find the actual subnet with:

```sh
docker network inspect bridge --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}'
```

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `upsc` says `Connection refused` | `upsd` is not running or `LISTEN` is wrong |
| `upsc` says `Driver not connected` | `usbhid-ups` is not running; run `sudo upsdrvctl start` |
| `upsc` says `Unknown UPS` | The section name in `ups.conf` does not match `ec850lcd` |
| Permission denied on USB | Reinstall the NUT udev rules or add your user to the `nut` group |
| Container cannot connect | `NUT_HOST` must resolve to the host; check `extra_hosts` and the firewall |
| Wrong readings | Run `upsc ec850lcd` and compare with the driver's `usbhid-ups` log |
