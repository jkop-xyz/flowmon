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
- Per Data Source: enabled, up, warnings and the last successful collection.

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

SNMP Data Sources add each device's interfaces (`flowmon_interface_up`, `_speed_bits`, `_in_bits_per_second`, `_out_bits_per_second`, `_errors_last_hour`, labelled `source` and `interface`) and its Device Health (`flowmon_device_uptime_seconds`, `_cpu_percent`, `_memory_used_bytes`, `_memory_total_bytes`, `_wifi_clients`, and per `sensor` `flowmon_device_temperature_celsius`, `_voltage_volts`, `_current_amperes`, `_power_watts`, `_fan_rpm`). A device restart shows as `flowmon_device_uptime_seconds` dropping: `resets(flowmon_device_uptime_seconds[1h]) > 0`.

## GeoIP databases

Flowmon looks up each Remote IP's country and ASN Owner when its Flow is stored. It picks a database for each, in this order:

1. **Your own files.** Put MMDB files (MaxMind GeoLite2/GeoIP2 Country, City or ASN, or DB-IP Lite) in `/data/geoip`, or mount a directory there and set `FLOWMON_GEOIP_DIR`. Flowmon checks for new or changed files every hour and never replaces them.
2. **MaxMind GeoLite2**, once you enter your account ID and license key under Settings → GeoIP. A [free account](https://www.maxmind.com/en/geolite2/signup) gives you both. The key is stored encrypted with `secret.key`, and the download starts at once, then weekly. `FLOWMON_MAXMIND_ACCOUNT_ID` and `FLOWMON_MAXMIND_LICENSE_KEY` (or `FLOWMON_MAXMIND_LICENSE_KEY_FILE`) still work and lock the setting.
3. **DB-IP Lite**, downloaded weekly without an account. It is licensed under CC BY 4.0, so the UI credits DB-IP wherever its data shows.

Downloads go to `/data/geoip-downloads`. With downloads switched off in Settings (or `FLOWMON_GEOIP_DOWNLOAD=false`, which locks it), nothing is downloaded and only your own files are used. Settings shows which database is in use, how old it is, and why a download failed.

A Flow keeps the country and ASN Owner it was stored with. A newer database applies to Flows from then on; it doesn't rewrite history.

## Reverse DNS and RDAP

Reverse DNS names are looked up only for Remote IPs shown on screen, through a rate-limited queue and the host's DNS resolver. Registration details (RDAP) are fetched only when you open a Remote IP's page, from the regional registry the [IANA bootstrap files](https://data.iana.org/rdap/) name for it. Results are cached in the App Store: reverse DNS for 24 hours, RDAP for 7 days.

Both send the address you look at to your DNS resolver or to a registry (ARIN, RIPE NCC, APNIC, LACNIC or AFRINIC). Flows themselves never trigger a lookup.

## Local Host names

A Local Host is shown by its Host Name: a Host Label you set on the Local Hosts page wins, then its reverse DNS name, then the address.

- **Reverse DNS** for Local Hosts asks the server in `FLOWMON_LOCAL_DNS_SERVER`, or the container's resolver when it is unset. Point it at the server that knows your hosts by name, such as the MikroTik or a Pi-hole. Names are cached for an hour, because DHCP can move them.
- **Host Labels** are stored in the App Store. A label on a single address names that host. A label on a prefix, such as an IPv6 `/64` or a DHCP range written as a prefix, makes every address in it one Local Host. Use it for a device whose addresses rotate. A label on an address inside a labelled prefix keeps that address separate, because the longest prefix wins.

## DNS from AdGuard Home

Flowmon can read AdGuard Home's query log, to show which names each device looks up and, in later versions, which domains its traffic goes to. Nothing is collected until you add AdGuard Home as a Data Source, because DNS logs say much more about what people do than Flows. Flowmon polls `/control/querylog` every 15 seconds with AdGuard Home's own login, and keeps DNS Queries for the DNS retention under Settings → Retention (7 days unless changed). The first poll reaches back one day. If Flowmon is off longer than AdGuard Home keeps its log, that time has no DNS and the source shows a warning.

Add it under Settings → AdGuard Home: its address, and a user AdGuard Home accepts. Test shows its version and how long it keeps its log. Or set it with environment variables, which lock it in the UI:

```sh
FLOWMON_SOURCE_ADGUARD_KIND=adguard
FLOWMON_SOURCE_ADGUARD_URL=http://192.168.10.2:3000
FLOWMON_SOURCE_ADGUARD_USERNAME=admin
FLOWMON_SOURCE_ADGUARD_PASSWORD=...
FLOWMON_SOURCE_ADGUARD_SKIP_TLS_VERIFY=false   # true for https with a self-signed certificate
```

Per-device DNS needs devices to ask AdGuard Home directly. If they ask the router and the router forwards to AdGuard Home, every query seems to come from the router: Flowmon warns when 90 % or more come from an Exporter. Hand out AdGuard Home's address by DHCP instead, or turn on EDNS Client Subnet on the router. Also leave AdGuard Home's "Anonymize client IP" off.

Several AdGuard Home instances (for redundancy) can each be a source; their logs merge, and each query remembers which one answered.

Flowmon also names traffic by domain. Each Flow gets the name its device looked up last, within a day before the Flow started, whose answer held the Flow's Remote IP. A Flow that arrives before its lookup was polled is named within a minute. Domain totals (per device and Registrable Domain, such as `bbc.co.uk`) are counted 5 minutes after a Flow arrives, once its name is final, so domain charts end 5 minutes before now while everything else is live. Domains start when collection starts: traffic from before has none. Traffic to addresses nobody looked up (DNS over HTTPS, hard-coded addresses) is counted as "no DNS lookup".

## Settings and environment variables

Local Networks and retention can be changed under Settings → Local Networks and Settings → Retention. An environment variable wins: a field set by `FLOWMON_LOCAL_NETWORKS` or `FLOWMON_RETENTION_*` shows as locked. These settings are kept in the Flow Store, not the App Store, so collection never needs the App Store.

Data Sources (integrations other than Exporters) can be set by environment variables too. Each Source gets an ID of letters and digits, and its settings are the Source's fields in capitals:

```sh
FLOWMON_SOURCE_HOME_KIND=<kind>
FLOWMON_SOURCE_HOME_NAME="At home"   # optional, defaults to the ID
FLOWMON_SOURCE_HOME_ENABLED=true     # optional
FLOWMON_SOURCE_HOME_<SETTING>=...
```

Such a Source is shown as locked and isn't stored. Flowmon refuses to start when one names an unknown kind or setting, so a typo can't go unnoticed. Passwords of Sources added in the UI are encrypted with `secret.key` in the data directory, like SNMP credentials.

A change to Local Networks applies to Flows from then on, and charts mark when it happened. To classify stored Flows again, reprocess an Exporter on the Exporters page.

## When the App Store is unavailable

If Postgres (or the SQLite file) stops answering, Flowmon keeps collecting and storing Flows. Every chart and list keeps working. Flowmon checks the App Store every 15 seconds and logs once when it goes away and once when it is back. Until then:

- Every page shows a banner saying the App Store isn't answering.
- People who were logged in within the last hour stay logged in. New logins wait.
- Changes stored in the App Store are refused with a clear message: Host Labels, Service Names and accounts. Host Labels and Service Names already loaded keep working.
- Reverse DNS and RDAP results are cached in memory only.

## Disk and memory

The Flow Store grows with traffic and raw retention: raw Flows take by far the most space. Settings shows its size, how far back raw Flows reach, and DuckDB's memory use.

- **`FLOWMON_FLOW_STORE_MAX_GB`** caps it, for a small disk or SD card. Every ten minutes, while the Flow Store is over the cap, Flowmon drops the oldest day of DNS Queries (from DNS Data Sources such as AdGuard Home), then the next, and then the oldest day of raw Flows, before their retention ends. When no raw day is left to drop, it drops minute Rollup days older than a day. It never drops:
  - a raw day that may still receive late Flows (until its Watermark passes),
  - today's raw Flows or DNS Queries, or the last day of minute Rollups,
  - hour Rollups, which hold the long history and take little space.

  Every Flow is rolled up as it is stored, and a day's Top Remote IPs are built before its raw Flows go, so charts and top lists keep their whole history. Only the Flow Explorer and exact Top Remote IPs reach back less far. Settings warns while the cap drops data early. If what's left is still over the cap, a banner says so on every page; raise the cap or shorten Rollup retention.

  Dropped data's space is reused before the file grows, but the file doesn't shrink, so the file on disk can stay larger than the data. Leave headroom: the cap is checked every ten minutes and a backup needs space of its own (below).
- **`FLOWMON_FLOW_STORE_MEMORY_LIMIT`** (default `1GB`) bounds DuckDB's memory. Large queries spill to `/data/flows.duckdb.tmp/` rather than use more. Lower it on a small host, such as `400MB` on a Raspberry Pi with 1 GB.
- **`FLOWMON_FLOW_STORE_THREADS`** limits DuckDB's threads (default: one per CPU). Lower it so queries leave CPU for collection on a small host.

## Backups

Back up the `/data` volume as a whole. It holds:

| File | What it is |
|---|---|
| `flows.duckdb` | the Flow Store: raw Flows, Rollups, DNS Queries, Exporter approvals, Interfaces, Data Sources, Local Networks and retention |
| `flowmon.db` | the App Store, when you chose the built-in SQLite |
| `bootstrap.json` | where the App Store lives (mode 0600; it can contain the Postgres password) |
| `secret.key` | the key that encrypts Data Sources' stored passwords, such as AdGuard Home's and SNMP's. Without it, they have to be entered again |
| `geoip-downloads/` | downloaded GeoIP databases. No need to back them up; they are downloaded again |

If you chose **PostgreSQL** for the App Store, accounts, sessions, Host Labels, Device names and Service Names live there. Back them up with your Postgres backups. The two stores are backed up separately; restoring one without the other is safe, but accounts or Flows from after the older backup are missing.

Copying `flows.duckdb` while Flowmon runs can give a broken copy. Either stop Flowmon first (`docker compose stop`, or `systemctl stop flowmon`), or download a snapshot while it runs:

- **In the UI:** Settings → Databases → **Download a backup**.
- **From a script:** set `FLOWMON_BACKUP_TOKEN` (or `FLOWMON_BACKUP_TOKEN_FILE`) and send it as a bearer token:

  ```sh
  curl -fsS -H "Authorization: Bearer $FLOWMON_BACKUP_TOKEN" \
    -o flows-$(date +%F).duckdb http://flowmon.lan:8080/api/flow-store/backup
  ```

A snapshot holds everything committed when it started, taken in one transaction while collection keeps running. It's a complete DuckDB file, usually smaller than `flows.duckdb` because it has no free space. While it's taken, it needs about as much free space in `/data` as the Flow Store's data, and the download starts only when the snapshot is complete. One snapshot is taken at a time; a second request gets `409 Conflict`.

To restore a snapshot:

1. Stop Flowmon.
2. Replace `/data/flows.duckdb` with the snapshot file, and delete `/data/flows.duckdb.wal` if it exists.
3. Start Flowmon. Flows from after the snapshot are missing, and retention drops what has expired since.

A snapshot from an older Flowmon version is upgraded when it opens, like the Flow Store itself on an update.

## Updating

- **Docker:** `docker compose pull && docker compose up -d`.
- **Podman:** `podman auto-update`, or `systemctl restart flowmon` after a pull.

Flowmon migrates its stores on start, and it refuses to start on a store written by a newer version. So an update can't be undone by starting the older image again: once a newer version has started on `/data`, the older one won't open it.

Before you update:

1. Back up `/data` as described above, and the PostgreSQL database if the App Store is there.
2. Check which tag you run. `latest` moves to each new release, so `docker compose pull` and `podman auto-update` take you to it. To stay on a version, use its tag instead, for example `ghcr.io/jkop-xyz/flowmon:0.2`.

To go back to the older version after an update:

1. Stop Flowmon.
2. Restore the backup of `/data`, and of PostgreSQL if you use it.
3. Set the older version's tag and start Flowmon. What was collected after the backup is lost.
