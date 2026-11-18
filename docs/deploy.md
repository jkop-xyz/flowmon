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

Flowmon asks for an 8 MiB receive buffer on its UDP sockets (`FLOWMON_UDP_RECEIVE_BUFFER_MB`), so bursts from the router aren't dropped. The kernel caps that at `net.core.rmem_max`, whose default is about 208 KiB. A container can't raise it, so set it on the host:

```sh
echo 'net.core.rmem_max = 8388608' | sudo tee /etc/sysctl.d/90-flowmon.conf
sudo sysctl --system
```

Alternatively, give the container the `NET_ADMIN` capability (`cap_add: [NET_ADMIN]` in Compose, `AddCapability=NET_ADMIN` in a Quadlet). With it, Flowmon forces the buffer size past `rmem_max`.

The Exporters page shows the buffer each socket got and how many datagrams the kernel dropped because it was full. Flowmon's own losses are shown next to them: a full write queue, Flows that arrived too late, and failed writes.

## Collection health and Prometheus

If approved Exporters exist but no Flows arrive for `FLOWMON_NO_FLOWS_WARNING` (default `5m`, `off` to disable), every page shows a warning.

`/metrics` serves Prometheus metrics without a login:

- Per socket: datagrams, kernel drops and receive buffer.
- Per Exporter: datagrams, records, deviations, Watermark and clock offset.
- The write pipeline: Flows stored and dropped by reason, queue length and the last write.
- The Flow Store: disk size and raw days kept.
- Whether the App Store answers, and whether collection has gone quiet.

The metrics hold counters and Exporter addresses, not Flow contents. To require a token, set `FLOWMON_METRICS_TOKEN` (or `FLOWMON_METRICS_TOKEN_FILE`) and configure Prometheus to send it:

```yaml
scrape_configs:
  - job_name: flowmon
    authorization:
      credentials_file: /etc/prometheus/flowmon-token
    static_configs:
      - targets: ["flowmon.lan:8080"]
```

Useful alerts are `flowmon_collection_quiet == 1`, `increase(flowmon_collector_socket_drops_total[15m]) > 0` and `increase(flowmon_ingest_flows_dropped_total[15m]) > 0`.

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

## Local Host names

A Local Host is shown by its Host Name: a Host Label you set on the Local Hosts page wins, then its reverse DNS name, then the address.

- **Reverse DNS** for Local Hosts asks the server in `FLOWMON_LOCAL_DNS_SERVER`, or the container's resolver when it is unset. Point it at the server that knows your hosts by name, such as the MikroTik or a Pi-hole. Names are cached for an hour, because DHCP can move them.
- **Host Labels** are stored in the App Store. A label on a single address names that host. A label on a prefix, such as an IPv6 `/64` or a DHCP range written as a prefix, makes every address in it one Local Host. Use it for a device whose addresses rotate. A label on an address inside a labelled prefix keeps that address separate, because the longest prefix wins.

## Settings and environment variables

Local Networks and retention can be changed on the Settings page. An environment variable wins: a field set by `FLOWMON_LOCAL_NETWORKS` or `FLOWMON_RETENTION_*` shows as locked. These settings are kept in the Flow Store, not the App Store, so collection never needs the App Store.

A change to Local Networks applies to Flows from then on, and charts mark when it happened. To classify stored Flows again, reprocess an Exporter on the Exporters page.

## When the App Store is unavailable

If Postgres (or the SQLite file) stops answering, Flowmon keeps collecting and storing Flows. Every chart and list keeps working. Flowmon checks the App Store every 15 seconds and logs once when it goes away and once when it is back. Until then:

- Every page shows a banner saying the App Store isn't answering.
- People who were logged in within the last hour stay logged in. New logins wait.
- Changes stored in the App Store are refused with a clear message: Host Labels, Service Names and accounts. Host Labels and Service Names already loaded keep working.
- Reverse DNS and RDAP results are cached in memory only.

## Backups

Back up the `/data` volume as a whole. It holds:

| File | What it is |
|---|---|
| `flows.duckdb` | the Flow Store: raw Flows, Rollups, Exporter approvals, Interfaces, SNMP settings, Local Networks and retention |
| `flowmon.db` | the App Store, when you chose the built-in SQLite |
| `bootstrap.json` | where the App Store lives (mode 0600; it can contain the Postgres password) |
| `secret.key` | the key that encrypts stored SNMP credentials. Without it, they have to be entered again |
| `geoip-downloads/` | downloaded GeoIP databases. No need to back them up; they are downloaded again |

If you chose **PostgreSQL** for the App Store, accounts, sessions, Host Labels and Service Names live there. Back them up with your Postgres backups. The two stores are backed up separately; restoring one without the other is safe, but accounts or Flows from after the older backup are missing.

To get a consistent copy of `flows.duckdb`, stop the container first (`docker compose stop`, or `systemctl stop flowmon`). An online snapshot endpoint is planned (#18).

## Updating

- **Docker:** `docker compose pull && docker compose up -d`.
- **Podman:** `podman auto-update`, or `systemctl restart flowmon` after a pull.

Flowmon migrates its stores on start. It refuses to start on a store written by a newer version.
