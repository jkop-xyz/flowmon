# Running Flowmon

Flowmon is one container image, `ghcr.io/jkop-xyz/flowmon`, built for `linux/amd64` and `linux/arm64`.

| Tag | What it is |
|---|---|
| `latest`, `1.2.3`, `1.2` | releases |
| `edge`, `sha-<commit>` | the newest commit on `main` |

Everything it keeps is in one volume mounted at `/data`.

While the repository is private, so is the image. Log in once on the host that runs Flowmon, with a GitHub token that has the `read:packages` scope:

```sh
echo "$TOKEN" | docker login ghcr.io -u <github-user> --password-stdin    # or: podman login ghcr.io
```

## Docker (Compose)

```sh
curl -O https://raw.githubusercontent.com/jkop-xyz/flowmon/main/compose.yaml
docker compose up -d
docker logs flowmon        # the one-time setup token is printed here
```

Open `http://<host>:8080` and follow the setup screen.

## Podman (Quadlet)

Copy [`deploy/podman/flowmon.container`](../deploy/podman/flowmon.container) and [`deploy/podman/flowmon-data.volume`](../deploy/podman/flowmon-data.volume) to:

- **rootful:** `/etc/containers/systemd/`, then `systemctl daemon-reload && systemctl start flowmon`
- **rootless:** `~/.config/containers/systemd/`, then `systemctl --user daemon-reload && systemctl --user start flowmon`. Also run `loginctl enable-linger $USER` once, so the service keeps running when you're logged out.

`journalctl [--user] -u flowmon` shows the setup token. `podman auto-update` pulls new images, because the unit sets `AutoUpdate=registry`.

The Quadlet declares its own health check (`HealthCmd=flowmon healthcheck`), because Podman ignores the image's `HEALTHCHECK` for OCI images. `Notify=healthy` makes systemd wait for it.

## Why host networking

Both files use host networking (`network_mode: host`, `Network=host`). This is deliberate:

- Flowmon identifies each Exporter by its **real source address**. Rootless Docker and some Podman port-forwarding modes rewrite it, which merges every router into one Exporter.
- A published UDP port can keep a **stale conntrack entry** after the container restarts. A router that sends without pause then has its packets silently dropped until the entry expires (Docker, and netavark issue #1045).

With host networking, Flowmon listens directly on the host: UDP 2055 and 4739 for flow export, TCP 8080 for the web UI. Change these with `FLOWMON_FLOW_UDP_ADDRS` and `FLOWMON_HTTP_ADDR`, for example `FLOWMON_HTTP_ADDR=127.0.0.1:8080` behind a reverse proxy, together with `FLOWMON_TRUSTED_PROXIES`.

## UDP receive buffer

Flowmon asks for a 4 MiB receive buffer on its UDP sockets, so bursts from the router aren't dropped. The kernel caps that at `net.core.rmem_max`, whose default is about 208 KiB. A container can't raise it, so set it on the host:

```sh
echo 'net.core.rmem_max = 4194304' | sudo tee /etc/sysctl.d/90-flowmon.conf
sudo sysctl --system
```

## GeoIP databases

Flowmon looks up each Remote IP's country and ASN Owner when its Flow is stored. It picks a database for each, in this order:

1. **Your own files.** Put MMDB files (MaxMind GeoLite2/GeoIP2 Country, City or ASN, or DB-IP Lite) in `/data/geoip`, or mount a directory there and set `FLOWMON_GEOIP_DIR`. Flowmon checks for new or changed files every hour and never replaces them.
2. **MaxMind GeoLite2**, if you set `FLOWMON_MAXMIND_ACCOUNT_ID` and `FLOWMON_MAXMIND_LICENSE_KEY` (or `FLOWMON_MAXMIND_LICENSE_KEY_FILE`). A [free account](https://www.maxmind.com/en/geolite2/signup) gives you both. Downloaded weekly.
3. **DB-IP Lite**, downloaded weekly without an account. It is licensed under CC BY 4.0, so the UI credits DB-IP wherever its data shows.

Downloads go to `/data/geoip-downloads`. With `FLOWMON_GEOIP_DOWNLOAD=false`, nothing is downloaded and only your own files are used. Settings shows which database is in use, how old it is, and why a download failed.

A Flow keeps the country and ASN Owner it was stored with. A newer database applies to Flows from then on; it doesn't rewrite history.

## Reverse DNS and RDAP

Reverse DNS names are looked up only for Remote IPs shown on screen, through a rate-limited queue and the host's DNS resolver. Registration details (RDAP) are fetched only when you open a Remote IP's page, from the regional registry the [IANA bootstrap files](https://data.iana.org/rdap/) name for it. Results are cached in the App Store: reverse DNS for 24 hours, RDAP for 7 days.

Both send the address you look at to your DNS resolver or to a registry (ARIN, RIPE NCC, APNIC, LACNIC or AFRINIC). Flows themselves never trigger a lookup.

## Backups

Back up the `/data` volume as a whole. It holds:

| File | What it is |
|---|---|
| `flows.duckdb` | the Flow Store: raw Flows, Rollups, Exporter approvals, Interfaces, SNMP settings |
| `flowmon.db` | the App Store, when you chose the built-in SQLite |
| `bootstrap.json` | where the App Store lives (mode 0600; it can contain the Postgres password) |
| `secret.key` | the key that encrypts stored SNMP credentials. Without it, they have to be entered again |
| `geoip-downloads/` | downloaded GeoIP databases. No need to back them up; they are downloaded again |

If you chose **PostgreSQL** for the App Store, accounts and settings live there. Back them up with your Postgres backups. The two stores are backed up separately; restoring one without the other is safe, but accounts or Flows from after the older backup are missing.

To get a consistent copy of `flows.duckdb`, stop the container first (`docker compose stop`, or `systemctl stop flowmon`). An online snapshot endpoint is planned (#18).

## Updating

- **Docker:** `docker compose pull && docker compose up -d`.
- **Podman:** `podman auto-update`, or `systemctl restart flowmon` after a pull.

Flowmon migrates its stores on start. It refuses to start on a store written by a newer version.
