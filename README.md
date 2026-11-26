# Flowmon

A self-hosted dashboard for network traffic. Flowmon collects IPFIX and NetFlow from your router (MikroTik first, but any exporter that follows the standard), and shows where traffic goes and who is on the other end: country, ASN owner, reverse DNS and registration details.

> **Status: early development.** Flowmon receives IPFIX and NetFlow v9 and v5 over UDP, stores Flows and minute/hour Rollups in an embedded DuckDB under `/data` with each Remote IP's country and ASN Owner, and shows bandwidth, a world map and the top countries, ASN Owners, Local Hosts, Remote IPs, protocols and Service Ports on the Overview. Remote IPs show their reverse DNS names, and each has a page with its registration details (RDAP), the Local Hosts that talked to it and its Flows, also for ranges older than raw retention through hourly and daily Top Remote IPs. Local Hosts show Host Names, from Host Labels you set or from reverse DNS against your own resolver, and each has a page with its traffic over time, Peers and Service Ports. Service Ports carry Service Names from the IANA registry, or your own from Settings. The Exporters page shows each Exporter with its Templates. The Flow Explorer filters, sorts and pages raw Flows and shows every field each Flow arrived with. The Exporters page shows what each socket received and dropped, and `/metrics` serves Prometheus metrics. Settings shows the Flow Store's size and takes online backups. Internal Exporters, such as a hypervisor, add traffic the router can't see without counting anything twice ([guide](docs/internal-exporters.md)). IPFIX over TCP and SCTP and RFC 6313 structured lists come later; progress is tracked in [issue #1](https://github.com/jkop-xyz/flowmon/issues/1).

## Run it

```sh
docker run -d --name flowmon --network host -v flowmon-data:/data --restart unless-stopped ghcr.io/jkop-xyz/flowmon:latest
```

Or use [`compose.yaml`](compose.yaml), or the Podman Quadlet in [`deploy/podman/`](deploy/podman). [docs/deploy.md](docs/deploy.md) explains both, plus host networking, the UDP buffer setting, backups and updates. An update can't be undone by starting the older image: back up `/data` before you update, or pin a version's tag, such as `:2`, instead of `latest` ([how](docs/deploy.md#updating)).

Then open <http://localhost:8080>. The first visit runs setup: enter the one-time setup token from the logs (`docker logs flowmon`), choose where accounts and settings live (built-in SQLite, or your own PostgreSQL), and create the admin account. Forgot the password? `docker exec -it flowmon flowmon admin reset-password admin`.

Setting up a MikroTik? See [docs/mikrotik-setup.md](docs/mikrotik-setup.md) (also in the app, linked from the Exporters page).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `FLOWMON_HTTP_ADDR` | `:8080` | Address for the web UI and API |
| `FLOWMON_DATA_DIR` | `/data` | Where Flowmon keeps its data |
| `FLOWMON_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `FLOWMON_LOG_FORMAT` | `text` | `text` or `json` |
| `FLOWMON_FLOW_UDP_ADDRS` | `:2055,:4739` | Comma-separated UDP addresses to receive flow export on. Both ports accept any version; the datagram says which it is |
| `FLOWMON_LOCAL_NETWORKS` | none | Extra Local Networks (comma-separated CIDRs) on top of the defaults: RFC 1918, ULA and link-local. 100.64.0.0/10 isn't a default; add it if you run Tailscale or similar |
| `FLOWMON_FLOW_STORE_MEMORY_LIMIT` | `1GB` | DuckDB memory limit for the Flow Store |
| `FLOWMON_FLOW_STORE_THREADS` | DuckDB's default | DuckDB threads for the Flow Store |
| `FLOWMON_FLOW_STORE_MAX_GB` | no cap | Keep the Flow Store under this many GiB, e.g. `20`, by dropping the oldest raw days before their retention ends (see the deploy guide) |
| `FLOWMON_RETENTION_RAW` | `7d` | How long raw Flows are kept (whole days are dropped) |
| `FLOWMON_RETENTION_MINUTE` | `90d` | How long minute Rollups are kept |
| `FLOWMON_RETENTION_HOUR` | `730d` | How long hour Rollups are kept |
| `FLOWMON_RETENTION_DNS` | `7d` | How long DNS Queries from DNS Data Sources (such as AdGuard Home) are kept (whole days are dropped) |
| `FLOWMON_WATERMARK` | learned | Override the per-Exporter Watermark (how long after a minute its data may still change), e.g. `5m` |
| `FLOWMON_DATABASE_URL` | setup screen | App Store location, e.g. `sqlite:///data/flowmon.db` or `postgres://flowmon@db.lan:5432/lab?sslmode=require&search_path=flowmon`. When set, it overrides and locks the setup screen's choice |
| `FLOWMON_DATABASE_PASSWORD_FILE` | none | File holding the Postgres password (e.g. a Docker secret) |
| `FLOWMON_ADMIN_USERNAME`, `FLOWMON_ADMIN_PASSWORD` (or `_FILE`) | none | Create the admin account on first start instead of in the setup screen |
| `FLOWMON_TRUSTED_PROXIES` | none | CIDRs of reverse proxies whose `X-Forwarded-For`/`-Proto` headers are trusted |
| `FLOWMON_SECURE_COOKIES` | `auto` | `auto` marks the session cookie Secure when the browser used HTTPS (directly or via a trusted proxy); `always` or `never` force it |
| `FLOWMON_EXPORTER_AUTO_ACCEPT` | `false` | Approve every new Exporter without asking. Otherwise a new Exporter waits on the Exporters page, its Templates kept and its Flows dropped |
| `FLOWMON_EXPORTER_ALLOWLIST` | none | CIDRs whose Exporters are approved without asking, e.g. `192.168.10.1/32` |
| `FLOWMON_MAX_EXPORTERS` | `64` | Most Exporters tracked; datagrams from further sources are ignored and counted |
| `FLOWMON_UDP_RECEIVE_BUFFER_MB` | `8` | Receive buffer to ask for on each UDP socket; the kernel caps it at `net.core.rmem_max` (see the deploy guide) |
| `FLOWMON_NO_FLOWS_WARNING` | `5m` | Warn in the UI when no Flows arrive for this long; `off` disables it |
| `FLOWMON_METRICS_TOKEN` (or `_FILE`) | none | Bearer token required on `/metrics`; without it, `/metrics` is open |
| `FLOWMON_BACKUP_TOKEN` (or `_FILE`) | none | Bearer token that downloads Flow Store backups from `/api/flow-store/backup` without logging in, for scheduled backups |
| `FLOWMON_GEOIP_DIR` | `/data/geoip` | Your own GeoIP MMDB files (country, city or ASN, from MaxMind or DB-IP). They win over downloads |
| `FLOWMON_GEOIP_DOWNLOAD` | `true` | Download GeoIP databases weekly: MaxMind GeoLite2 with an account, DB-IP Lite otherwise. `false` uses only your own files. Can also be switched in Settings; set here, it is locked |
| `FLOWMON_MAXMIND_ACCOUNT_ID`, `FLOWMON_MAXMIND_LICENSE_KEY` (or `_FILE`) | none | Your free MaxMind account, for GeoLite2 instead of DB-IP Lite. Usually entered in Settings → GeoIP instead; set here, it is locked |
| `FLOWMON_LOCAL_DNS_SERVER` | system resolver | The DNS server that knows your Local Hosts' names, such as the router or a Pi-hole: `192.168.88.1` or `[fd00::1]:53` |
| `FLOWMON_SOURCE_<ID>_KIND`, `_NAME`, `_ENABLED`, `_<SETTING>` | none | A Data Source set by environment variables instead of on the Sources page, which then can't be changed there. `<ID>` is letters and digits (see the deploy guide) |
| `FLOWMON_DEBUG_CAPTURE` | off | Path of a pcap file to record received datagrams to (see below) |
| `FLOWMON_DEBUG_CAPTURE_MAX_MB` | `64` | Size at which the capture file is rotated |
| `FLOWMON_DEBUG_CAPTURE_FILES` | `3` | Capture files kept, including the current one |
| `FLOWMON_DEBUG_CAPTURE_MAX_DURATION` | `1h` | Capturing stops this long after start |

### SNMP and Reconciliation

With SNMP on the router and its WAN Interface marked, the Overview leads with the line's load now from the router's own counters (against the line speed set under Settings → Internet line), who is using it in the last 5 minutes by Device and domain, each SNMP device's health and interface problems, and DNS lookups and blocks. Without SNMP it charts bandwidth from Flows, as before.

Any device can be polled over SNMP (v3 recommended, or v2c): add it under Settings → SNMP devices. Every minute Flowmon reads all its interfaces into Interface Counters: traffic, errors, discards, speed and status. It also reads the device's ARP and IPv6 neighbor tables: each address-to-MAC mapping in the Local Networks keeps that Device's address history current and gives Flows to the address their Device, so quiet devices with little or no traffic are listed under Devices too. When the device is an Exporter (its address, or the Exporter set on the source), SNMP fills in its interface names (`ifName`), and for every WAN Interface Flowmon compares the router's own 64-bit interface counters (minus layer-2 overhead per packet) with the bytes in Flows, per hour. A gap above 15% for 3 hours in a row is flagged: usually hardware offload that Traffic Flow can't see, a long active timeout, or the wrong overhead preset, which is set on the source. SNMP settings an Exporter had in earlier versions become an SNMP Data Source on the first start. SNMP credentials are stored encrypted with a key kept in `/data/secret.key`; back that file up with the rest of `/data`.

### Supported exports

Flowmon decodes IPFIX, NetFlow v9 and NetFlow v5 on any of its UDP ports; each datagram says which it is. IPFIX and NetFlow v9 carry whatever fields the Template lists, including the NAT addresses Flowmon uses to tell which Local Host a reply belongs to. NetFlow v5 has a fixed record with IPv4 addresses, ports and interfaces only: no IPv6 and no NAT addresses. On a router that NATs, a v5 exporter reports what it sees on the interface it watches, so replies can be attributed to the router's WAN address rather than the Local Host. Use IPFIX or NetFlow v9 when the exporter offers them.

Biflow exporters (RFC 5103, such as YAF) send both directions of a connection in one record. Flowmon stores each as two Flows, one per direction, so they count the same as from exporters that send each direction separately.

Exporters that sample packets (count 1 in N) are supported: Flowmon reads the rate from the records, the exporter's Options records or the NetFlow v5 header, and scales bytes and packets up by N. An exporter that samples without saying so can have its rate set on the Exporters page. Sampled traffic is an estimate, and charts, lists and the Flow Explorer mark it with ≈. Counting every packet (sampling off) is more accurate where the router can afford it.

### Debug capture

`FLOWMON_DEBUG_CAPTURE=/data/debug/capture.pcap` records every datagram Flowmon receives, wrapped in IP and UDP headers so Wireshark and tshark decode it as IPFIX or NetFlow. Use it when an Exporter shows up as "0% decoded" or its numbers look wrong. Captures contain who talked to whom on your network: the files are created readable only by Flowmon's user, the capture stops on its own, and you should anonymise a capture before sharing it.

`flowmon healthcheck` exits 0 when the local server is healthy. Use it in run configs: Podman ignores the image's `HEALTHCHECK` for OCI images.

## Develop

You need Go 1.27 and Node 26.

```sh
make build        # build the UI, then bin/flowmon with the UI embedded
make test         # Go vet and tests, frontend lint
make dev          # run the server on :8080
cd web && npm run dev   # UI with hot reload on :5173, proxying /api to :8080
go run ./internal/devtools/sendipfix -addr 127.0.0.1:4739   # synthetic MikroTik-like IPFIX
go run ./internal/devtools/sendipfix -addr 127.0.0.1:2055 -version 9   # the same as NetFlow v9 (or -version 5)
```

Captures of real exporters (`.pcap`, from tcpdump or `FLOWMON_DEBUG_CAPTURE`) go in [`internal/collector/testdata/captures/`](internal/collector/testdata/captures). `go test ./internal/collector` decodes each one and checks that every record becomes a Flow with the times the exporter gave.

The design lives in [`GLOSSARY.md`](GLOSSARY.md) (the domain language) and [`docs/adr/`](docs/adr) (the decisions and why they were made).

## Licence

[Apache-2.0](LICENSE). Flowmon uses GeoIP data from MaxMind GeoLite2 (downloaded with your own licence key) or [DB-IP](https://db-ip.com) Lite (CC BY 4.0, attribution shown in the UI), or files you provide; see [docs/deploy.md](docs/deploy.md#geoip-databases). Neither is bundled in the image. The world map uses [Natural Earth](https://www.naturalearthdata.com) shapes (public domain) via world-atlas.
