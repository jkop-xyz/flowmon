# Configuration

Flowmon is set with environment variables. All of them are optional: with none set, Flowmon listens for flow export on UDP 2055 and 4739, serves the UI on port 8080 and keeps everything in `/data`. Data Sources and GeoIP can also be set in the UI under Settings; a value set here locks that setting there.

## Environment variables

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
| `FLOWMON_FLOW_STORE_MAX_GB` | no cap | Keep the Flow Store under this many GiB, e.g. `20`, by dropping the oldest raw days before their retention ends (see the [deploy guide](deploy.md)) |
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
| `FLOWMON_EXPORTER_ALLOWLIST` | none | CIDRs whose Exporters are approved without asking, e.g. `192.168.88.1/32` |
| `FLOWMON_MAX_EXPORTERS` | `64` | Most Exporters tracked; datagrams from further sources are ignored and counted |
| `FLOWMON_UDP_RECEIVE_BUFFER_MB` | `8` | Receive buffer to ask for on each UDP socket; the kernel caps it at `net.core.rmem_max` (see the [deploy guide](deploy.md)) |
| `FLOWMON_NO_FLOWS_WARNING` | `5m` | Warn in the UI when no Flows arrive for this long; `off` disables it |
| `FLOWMON_METRICS_TOKEN` (or `_FILE`) | none | Bearer token required on `/metrics`; without it, `/metrics` is open |
| `FLOWMON_BACKUP_TOKEN` (or `_FILE`) | none | Bearer token that downloads Flow Store backups from `/api/flow-store/backup` without logging in, for scheduled backups |
| `FLOWMON_GEOIP_DIR` | `/data/geoip` | Your own GeoIP MMDB files (country, city or ASN, from MaxMind or DB-IP). They win over downloads |
| `FLOWMON_GEOIP_DOWNLOAD` | `true` | Download GeoIP databases weekly: MaxMind GeoLite2 with an account, DB-IP Lite otherwise. `false` uses only your own files. Can also be switched in Settings; set here, it is locked |
| `FLOWMON_MAXMIND_ACCOUNT_ID`, `FLOWMON_MAXMIND_LICENSE_KEY` (or `_FILE`) | none | Your free MaxMind account, for GeoLite2 instead of DB-IP Lite. Usually entered in Settings → GeoIP instead; set here, it is locked |
| `FLOWMON_LOCAL_DNS_SERVER` | system resolver | The DNS server that knows your Local Hosts' names, such as the router or a Pi-hole: `192.168.88.1` or `[fd00::1]:53` |
| `FLOWMON_SOURCE_<ID>_KIND`, `_NAME`, `_ENABLED`, `_<SETTING>` | none | A Data Source set by environment variables instead of in Settings, which then can't be changed there. `<ID>` is letters and digits (see the [deploy guide](deploy.md)) |
| `FLOWMON_DEBUG_CAPTURE` | off | Path of a pcap file to record received datagrams to (see below) |
| `FLOWMON_DEBUG_CAPTURE_MAX_MB` | `64` | Size at which the capture file is rotated |
| `FLOWMON_DEBUG_CAPTURE_FILES` | `3` | Capture files kept, including the current one |
| `FLOWMON_DEBUG_CAPTURE_MAX_DURATION` | `1h` | Capturing stops this long after start |

## SNMP and Reconciliation

With SNMP on the router and its WAN Interface marked, the Overview leads with the line's load now from the router's own counters (against the line speed set under Settings → Internet line), who is using it in the last 5 minutes by Device and domain, each SNMP device's health and interface problems, and DNS lookups and blocks. Without SNMP it charts bandwidth from Flows, as before.

Any device can be polled over SNMP (v3 recommended, or v2c): add it under Settings → SNMP devices. Every minute Flowmon reads all its interfaces into Interface Counters: traffic, errors, discards, speed and status. It also reads the device's ARP and IPv6 neighbor tables: each address-to-MAC mapping in the Local Networks keeps that Device's address history current and gives Flows to the address their Device, so quiet devices with little or no traffic are listed under Devices too. When the device is an Exporter (its address, or the Exporter set on the source), SNMP fills in its interface names (`ifName`), and for every WAN Interface Flowmon compares the router's own 64-bit interface counters (minus layer-2 overhead per packet) with the bytes in Flows, per hour. A gap above 15% for 3 hours in a row is flagged: usually hardware offload that Traffic Flow can't see, a long active timeout, or the wrong overhead preset, which is set on the source. SNMP settings an Exporter had in earlier versions become an SNMP Data Source on the first start. SNMP credentials are stored encrypted with a key kept in `/data/secret.key`; back that file up with the rest of `/data`.

## Supported exports

Flowmon decodes IPFIX, NetFlow v9 and NetFlow v5 on any of its UDP ports; each datagram says which it is. IPFIX and NetFlow v9 carry whatever fields the Template lists, including the NAT addresses Flowmon uses to tell which Local Host a reply belongs to. NetFlow v5 has a fixed record with IPv4 addresses, ports and interfaces only: no IPv6 and no NAT addresses. On a router that NATs, a v5 exporter reports what it sees on the interface it watches, so replies can be attributed to the router's WAN address rather than the Local Host. Use IPFIX or NetFlow v9 when the exporter offers them.

Biflow exporters (RFC 5103, such as YAF) send both directions of a connection in one record. Flowmon stores each as two Flows, one per direction, so they count the same as from exporters that send each direction separately.

Exporters that sample packets (count 1 in N) are supported: Flowmon reads the rate from the records, the exporter's Options records or the NetFlow v5 header, and scales bytes and packets up by N. An exporter that samples without saying so can have its rate set on the Exporters page. Sampled traffic is an estimate, and charts, lists and the Flow Explorer mark it with ≈. Counting every packet (sampling off) is more accurate where the router can afford it.

## Debug capture

`FLOWMON_DEBUG_CAPTURE=/data/debug/capture.pcap` records every datagram Flowmon receives, wrapped in IP and UDP headers so Wireshark and tshark decode it as IPFIX or NetFlow. Use it when an Exporter shows up as "0% decoded" or its numbers look wrong. Captures contain who talked to whom on your network: the files are created readable only by Flowmon's user, the capture stops on its own, and you should anonymise a capture before sharing it.

## Health check

`flowmon healthcheck` exits 0 when the local server is healthy. Use it in run configs: Podman ignores the image's `HEALTHCHECK` for OCI images.

